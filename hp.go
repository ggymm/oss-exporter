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
	c.GetSystemInfo()
	// 版本信息
	c.GetVersionInfo()
	// 组件状态
	c.GetComponentState()
	// 磁盘信息
	c.GetDiskInfo()

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

func (c *HP) GetSystemInfo() {
	c.Log.Debug("[REST]系统信息")

	requestUrl := fmt.Sprintf("%s/v3/api/show/system?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求系统信息失败, error: %v", err)
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析系统信息响应数据失败, error: %v", err)
		}
		element := doc.FindElement("/RESPONSE/OBJECT/PROPERTY[@name='vendor-name']")
		c.CrawlerData.VendorName = element.Text()
	}
}

func (c *HP) GetVersionInfo() {
	c.Log.Debug("[REST]版本信息")

	requestUrl := fmt.Sprintf("%s/v3/api/show/version?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求版本信息失败, error: %v", err)
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析版本信息响应数据失败, error: %v", err)
		}
		elements := doc.FindElements("/RESPONSE/OBJECT[@basetype='versions']/PROPERTY[@name='bundle-version']")
		for i := 0; i < len(elements); i++ {
			c.CrawlerData.BundleVersions = append(c.CrawlerData.BundleVersions, elements[i].Text())
		}
	}
}

func (c *HP) GetComponentState() {
	c.Log.Debug("[REST]组件状态(不包括磁盘)")

	requestUrl := fmt.Sprintf("%s/v3/api/show/enclosures?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求版本信息失败, error: %v", err)
	} else {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(data); err != nil {
			c.Log.Errorf("[REST]解析组件状态响应数据失败, error: %v", err)
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
			for i := 0; i < len(childElems); i++ {
				stateElems := cElems[i].FindElements(childElems[i])
				for j := 0; j < len(stateElems); j++ {
					id := stateElems[j].FindElement("./PROPERTY[@name='durable-id']").Text()
					health := stateElems[j].FindElement("./PROPERTY[@name='health']").Text()

					state := make(map[string]interface{})
					state["id"] = id
					state["health"] = health
					switch i {
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
	}
}

func (c *HP) GetDiskInfo() {
	c.Log.Debug("[REST]磁盘信息")

	// view-source:https://7.3.20.19/v3/api/show/disks?_=1633914554615

}
