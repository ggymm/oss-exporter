package main

import (
	"crypto/md5"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	"go.uber.org/zap"
)

var (
	//go:embed config/hp_account
	HPAccount string
	//go:embed config/hp_password
	HPPassword string
)

var (
	//go:embed hp_parse.js
	HPScript string
)

type HPCrawlerData struct {
	VendorName     string   `json:"vendorName"`
	BundleVersions []string `json:"bundleVersions"`

	ControllerStates    []interface{} `json:"controllerStates"`    // 控制器状态
	NetworkStates       []interface{} `json:"networkStates"`       // 网络端口状态
	PortStates          []interface{} `json:"portStates"`          // 主机端口状态
	ExpanderPortStates  []interface{} `json:"expanderPortStates"`  // 扩展端口状态
	CompactFlashStates  []interface{} `json:"compactFlashStates"`  // CompactFlash状态
	PowerSuppliesStates []interface{} `json:"powerSuppliesStates"` // 电源状态
	FanStates           []interface{} `json:"fanStates"`           // 风扇状态

	DiskInfo []interface{} `json:"diskInfo"`

	SizeTotal   int64 `json:"sizeTotal"`   // 总容量
	SizeSpares  int64 `json:"sizeSpares"`  // 全局备用磁盘(spaceSpares)
	SizeVirtual int64 `json:"sizeVirtual"` // 虚拟磁盘组(spaceVirtualPools)

	VirtPoolAllocSizeTotal int64 `json:"virtPoolAllocSizeTotal"` // 已分配(spaceVirtualAlloc)(poolsSet)
	VirtUnallocSizeTotal   int64 `json:"virtUnallocSizeTotal"`   // 未分配(spaceVirtualUnalloc)(volumeGroupsSet)
}

func (h *HPCrawlerData) PrintStr() {
	data, _ := json.MarshalIndent(&h, "", "  ")
	fmt.Println(string(data))
}

type HP struct {
	Log *zap.SugaredLogger

	AuthFile   string
	AuthCookie string

	Host     string
	Username string
	Password string

	CrawlerData *HPCrawlerData
}

func NewHPCrawler() (*HP, error) {
	c := new(HP)

	logger, err := NewLogger("hp.log")
	if err != nil {
		return nil, err
	}
	c.Log = logger

	c.AuthFile = "cookie/hp.cookie"

	c.Host = "https://7.3.20.19"
	c.Username = HPAccount
	c.Password = HPPassword

	c.CrawlerData = new(HPCrawlerData)

	return c, nil
}

func (c *HP) Start() {
	if isExist(c.AuthFile) {
		c.Log.Debug("检查到授权信息文件")
		if cookie, err := ioutil.ReadFile(c.AuthFile); err != nil {
			c.Log.Errorf("读取授权信息文件失败, 需要重新登陆, error: %v", err)
			if err := c.Login(); err != nil {
				c.Log.Errorf("登陆失败, 请重试, error: %v", err)
				return
			}
		} else {
			// 需要判断授权是否过期
			if len(cookie) > 0 {
				c.AuthCookie = string(cookie)
			} else {
				c.Log.Debug("授权信息文件为空, 执行登陆操作")
				if err := c.Login(); err != nil {
					c.Log.Errorf("登陆失败, 请重试, error: %v", err)
					return
				}
			}
		}
	} else {
		c.Log.Debug("未检查到授权信息文件, 执行登陆操作")
		if err := c.Login(); err != nil {
			c.Log.Errorf("登陆失败, 请重试, error: %v", err)
			return
		}
	}

	// 系统信息
	if err := c.GetSystemInfo(); err != nil {
		return
	}
	// 版本信息
	if err := c.GetVersionInfo(); err != nil {
		return
	}
	// 组件状态
	if err := c.GetComponentState(); err != nil {
		return
	}
	// 磁盘信息
	if err := c.GetDiskInfo(); err != nil {
		return
	}
	// 存储池信息
	if err := c.GetPoolInfo(); err != nil {
		return
	}
	// 卷组信息
	if err := c.GetVolumeGroupInfo(); err != nil {
		return
	}

	c.CrawlerData.PrintStr()
}

func (c *HP) Login() error {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}

	loginUrl := c.Host + "/v3/api/"
	h := md5.New()
	h.Write([]byte(c.Username + "_" + c.Password))
	encodeParam := "/api/login/" + hex.EncodeToString(h.Sum(nil))
	request, _ := http.NewRequest("POST", loginUrl, strings.NewReader(encodeParam))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if resp, err := client.Do(request); err != nil {
		c.Log.Errorf("请求失败, params: %v, error: %v", encodeParam, err)
		return err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()

		if code := resp.StatusCode; code != 200 {
			c.Log.Errorf("执行登陆请求失败, 错误码: %d", code)
			return err
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			c.Log.Errorf("读取请求体数据失败, params: %v, error: %v", encodeParam, err)
			return err
		}
		// 解析授权信息
		doc := etree.NewDocument()
		if err := doc.ReadFromBytes(body); err != nil {
			c.Log.Errorf("解析请求结果数据失败, error: %v", err)
			return err
		}
		element := doc.FindElement("/RESPONSE/OBJECT/PROPERTY[@name='response']")
		session := element.Text()
		if len(session) > 0 {
			c.AuthCookie = "wbisessionkey=" + session + ";wbiusername=manage"
			if err := os.MkdirAll(filepath.Dir(c.AuthFile), os.ModePerm); err != nil {
				c.Log.Errorf("创建授权信息文件失败, error: %v", err)
				return err
			}
			if err := ioutil.WriteFile(c.AuthFile, []byte(c.AuthCookie), os.ModePerm); err != nil {
				c.Log.Errorf("写入授权信息到文件失败, error: %v", err)
				return err
			}

			// 设置本地语言为中文
			localRequest, _ := http.NewRequest("POST", loginUrl, strings.NewReader("/api/set/cli-parameters/locale/Chinese-Simplified"))
			localRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			localRequest.Header.Set("Cookie", c.AuthCookie)
			if localResp, err := client.Do(localRequest); err != nil {
				c.Log.Errorf("设置本地语言为中文失败, error: %v", err)
			} else {
				defer func() {
					_ = localResp.Body.Close()
				}()
				if code := localResp.StatusCode; code != 200 {
					c.Log.Errorf("设置本地语言为中文失败, 错误码: %d", code)
				} else {
					c.Log.Debug("设置本地语言为中文成功")
				}
			}

			return nil
		} else {
			return errors.New("登陆失败，未获取到授权信息")
		}
	}
}

func (c *HP) RequestJson(method, url string, params io.Reader) (string, error) {
	// 构造请求客户端
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}

	request, err := http.NewRequest(method, url, params)
	if err != nil {
		c.Log.Errorf("构造请求失败, error: %v", err)
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("Cookie", c.AuthCookie)

	if resp, err := client.Do(request); err != nil {
		c.Log.Errorf("发送请求失败, url: %v, error: %v", url, err)
		return "", err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()

		// 判断HTTP状态码
		if code := resp.StatusCode; code != 200 {
			c.Log.Errorf("发送请求失败, url, %s, 错误码: %d, 错误信息: %v", url, code, resp.Header)
			return "", errors.New("发送请求失败")
		}
		if body, err := ioutil.ReadAll(resp.Body); err != nil {
			c.Log.Errorf("读取请求体数据失败, url: %s, error: %v", url, err)
			return "", err
		} else {
			// 判断业务状态码
			doc := etree.NewDocument()
			if err := doc.ReadFromBytes(body); err != nil {
				c.Log.Errorf("解析请求体数据失败, url, %s, 错误信息: %v", url, err)
				return "", err
			}
			response := doc.FindElement("/RESPONSE/OBJECT[@basetype='status']/PROPERTY[@name='response']").Text()
			returnCode := doc.FindElement("/RESPONSE/OBJECT[@basetype='status']/PROPERTY[@name='return-code']").Text()
			if returnCode != "0" {
				if returnCode == "-10027" {
					_ = os.Remove(c.AuthFile)
					c.Log.Errorf("权限验证失败, 移除cookie文件, 请重新运行")
					return "", errors.New("权限验证失败")
				} else {
					c.Log.Errorf("请求失败, url: %s, 错误码: %s, 错误信息: %s", url, returnCode, response)
					return "", errors.New("请求失败")
				}
			} else {
				return string(body), nil
			}
		}
	}
}

func (c *HP) GetSystemInfo() error {
	c.Log.Debug("[REST]系统信息")

	requestUrl := fmt.Sprintf("%s/v3/api/show/system?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求系统信息失败, error: %v", err)
		return err
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析系统信息响应数据失败, error: %v", err)
			return err
		}
		element := doc.FindElement("/RESPONSE/OBJECT/PROPERTY[@name='vendor-name']")
		c.CrawlerData.VendorName = element.Text()

		return nil
	}
}

func (c *HP) GetVersionInfo() error {
	c.Log.Debug("[REST]版本信息")

	requestUrl := fmt.Sprintf("%s/v3/api/show/version?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求版本信息失败, error: %v", err)
		return err
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析版本信息响应数据失败, error: %v", err)
			return err
		}
		elements := doc.FindElements("/RESPONSE/OBJECT[@basetype='versions']/PROPERTY[@name='bundle-version']")
		for i := 0; i < len(elements); i++ {
			c.CrawlerData.BundleVersions = append(c.CrawlerData.BundleVersions, elements[i].Text())
		}

		return nil
	}
}

func (c *HP) GetComponentState() error {
	c.Log.Debug("[REST]组件状态(不包括磁盘)")

	requestUrl := fmt.Sprintf("%s/v3/api/show/enclosures?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求组件状态失败, error: %v", err)
		return err
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析组件状态响应数据失败, error: %v", err)
			return err
		}

		// 控制器状态
		cElems := doc.FindElements("/RESPONSE/OBJECT/OBJECT[@basetype='controllers']")
		for i := 0; i < len(cElems); i++ {
			id := cElems[i].FindElement("./PROPERTY[@name='controller-id']").Text()
			health := cElems[i].FindElement("./PROPERTY[@name='health']").Text()

			cState := make(map[string]interface{})
			cState["id"] = id
			cState["health"] = health
			c.CrawlerData.ControllerStates = append(c.CrawlerData.ControllerStates, cState)

			childElems := []string{
				"./OBJECT[@basetype='network-parameters']", // 网络端口状态
				"./OBJECT[@basetype='port']",               // 主机端口状态
				"./OBJECT[@basetype='expander-ports']",     // 扩展端口状态
				"./OBJECT[@basetype='compact-flash']",      // CompactFlash状态
			}
			for j := 0; j < len(childElems); j++ {
				stateElems := cElems[i].FindElements(childElems[j])
				for k := 0; k < len(stateElems); k++ {
					id := stateElems[k].FindElement("./PROPERTY[@name='durable-id']").Text()
					health := stateElems[k].FindElement("./PROPERTY[@name='health']").Text()

					state := make(map[string]interface{})
					state["id"] = id
					state["health"] = health
					switch j {
					case 0:
						c.CrawlerData.NetworkStates = append(c.CrawlerData.NetworkStates, state)
					case 1:
						c.CrawlerData.PortStates = append(c.CrawlerData.PortStates, state)
					case 2:
						c.CrawlerData.ExpanderPortStates = append(c.CrawlerData.ExpanderPortStates, state)
					case 3:
						c.CrawlerData.CompactFlashStates = append(c.CrawlerData.CompactFlashStates, state)
					}
				}
			}
		}

		// 电源状态
		psElems := doc.FindElements("/RESPONSE/OBJECT/OBJECT[@basetype='power-supplies']")
		for i := 0; i < len(psElems); i++ {
			id := psElems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
			health := psElems[i].FindElement("./PROPERTY[@name='health']").Text()

			psState := make(map[string]interface{})
			psState["id"] = id
			psState["health"] = health
			c.CrawlerData.PowerSuppliesStates = append(c.CrawlerData.PowerSuppliesStates, psState)

			// 风扇状态
			fElems := psElems[i].FindElements("./OBJECT[@basetype='fan']")
			for i := 0; i < len(fElems); i++ {
				id := fElems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
				health := fElems[i].FindElement("./PROPERTY[@name='health']").Text()

				fState := make(map[string]interface{})
				fState["id"] = id
				fState["health"] = health
				c.CrawlerData.FanStates = append(c.CrawlerData.FanStates, fState)
			}
		}

		return nil
	}
}

func (c *HP) GetDiskInfo() error {
	c.Log.Debug("[REST]磁盘信息")

	requestUrl := fmt.Sprintf("%s/v3/api/show/disks?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求磁盘信息失败, error: %v", err)
		return err
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析磁盘信息响应数据失败, error: %v", err)
			return err
		}

		elems := doc.FindElements("/RESPONSE/OBJECT[@basetype='drives']")
		for i := 0; i < len(elems); i++ {
			// ID
			id := elems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
			// 运行状态
			health := elems[i].FindElement("./PROPERTY[@name='health']").Text()
			// 描述
			description := elems[i].FindElement("./PROPERTY[@name='description']").Text()
			// 大小
			size := elems[i].FindElement("./PROPERTY[@name='size']").Text()
			// 状态
			status := elems[i].FindElement("./PROPERTY[@name='status']").Text()

			diskInfo := make(map[string]interface{})
			diskInfo["id"] = id
			diskInfo["health"] = health
			diskInfo["description"] = description
			diskInfo["size"] = size
			diskInfo["status"] = status
			c.CrawlerData.DiskInfo = append(c.CrawlerData.DiskInfo, diskInfo)

			// 使用情况
			usageNumeric := elems[i].FindElement("./PROPERTY[@name='usage-numeric']").Text()
			// 大小
			sizeNumeric := elems[i].FindElement("./PROPERTY[@name='size-numeric']").Text()
			sizeNumericInt, _ := strconv.ParseInt(sizeNumeric, 10, 64)

			c.CrawlerData.SizeTotal += sizeNumericInt * 512
			switch usageNumeric {
			case "2", "3":
				c.CrawlerData.SizeSpares += sizeNumericInt * 512
			case "9":
				c.CrawlerData.SizeVirtual += sizeNumericInt * 512
			}
		}

		return nil
	}
}

func (c *HP) GetPoolInfo() error {
	c.Log.Debug("[REST]存储池信息")

	requestUrl := fmt.Sprintf("%s/v3/api/show/pools?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求存储池信息失败, error: %v", err)
		return err
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析存储池信息响应数据失败, error: %v", err)
			return err
		}

		elems := doc.FindElements("/RESPONSE/OBJECT[@basetype='pools']")
		for i := 0; i < len(elems); i++ {
			// 页面大小（块）（8192）
			pageSize := elems[i].FindElement("./PROPERTY[@name='page-size-numeric']").Text()
			pageSizeInt, _ := strconv.Atoi(pageSize)
			// 分配的页数
			allocatedPages := elems[i].FindElement("./PROPERTY[@name='allocated-pages']").Text()
			allocatedPagesInt, _ := strconv.ParseInt(allocatedPages, 10, 64)

			c.CrawlerData.VirtPoolAllocSizeTotal += int64(pageSizeInt) * allocatedPagesInt * 512
		}

		return nil
	}
}

func (c *HP) GetVolumeGroupInfo() error {
	c.Log.Debug("[REST]卷组信息")

	requestUrl := fmt.Sprintf("%s/v3/api/show/volume-groups?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求卷组信息失败, error: %v", err)
		return err
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析存储池信息响应数据失败, error: %v", err)
			return err
		}

		var volumeTotalSize int64
		// 计算总页数
		elems := doc.FindElements("/RESPONSE/OBJECT/OBJECT[@basetype='volumes']")
		for i := 0; i < len(elems); i++ {
			sizeNumeric := elems[i].FindElement("./PROPERTY[@name='size-numeric']").Text()
			sizeNumericInt, _ := strconv.ParseInt(sizeNumeric, 10, 64)

			volumeTypeNumeric := elems[i].FindElement("./PROPERTY[@name='volume-type-numeric']").Text()
			if volumeTypeNumeric == "0" ||
				volumeTypeNumeric == "2" ||
				volumeTypeNumeric == "4" ||
				volumeTypeNumeric == "8" ||
				volumeTypeNumeric == "13" ||
				volumeTypeNumeric == "15" {
				volumeTotalSize += sizeNumericInt * 512
			}
		}

		c.CrawlerData.VirtUnallocSizeTotal = volumeTotalSize - c.CrawlerData.VirtPoolAllocSizeTotal

		return nil
	}
}
