package main

import (
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/buger/jsonparser"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

var (
	//go:embed config/dell_account
	DellAccount string
	//go:embed config/dell_password
	DellPassword string
)

type DellRpcRequest struct {
	Type            string   `json:"type"`
	PluginId        string   `json:"pluginId"`
	CorrelationId   string   `json:"correlationId"`
	MethodName      string   `json:"methodName"`
	MethodArguments []string `json:"methodArguments"`
	HandlerName     string   `json:"handlerName"`
}

type DellCrawlerData struct {
	UsedSpace  map[string]string `json:"usedSpace"`
	FreeSpace  map[string]string `json:"freeSpace"`
	TotalSpace map[string]string `json:"totalSpace"`

	PoolUsedSpace  map[string]string `json:"poolUsedSpace"`
	PoolFreeSpace  map[string]string `json:"poolFreeSpace"`
	PoolTotalSpace map[string]string `json:"poolTotalSpace"`

	EnclosureInfo []map[string]string `json:"enclosureInfo"`
	DiskInfo      []map[string]string `json:"diskInfo"`
	PortInfo      []map[string]string `json:"portInfo"`
}

func (h *DellCrawlerData) PrintStr() {
	data, _ := json.MarshalIndent(&h, "", "  ")
	fmt.Println(string(data))
}

type Dell struct {
	Log *zap.SugaredLogger

	AuthFile   string
	AuthCookie string

	Host   string
	WSHost string

	Username string
	Password string

	SerialNumber string

	wg          sync.WaitGroup
	wsConn      *websocket.Conn
	CrawlerData *DellCrawlerData
}

func NewDellCrawler() (*Dell, error) {
	c := new(Dell)

	logger, err := NewLogger("dell.log")
	if err != nil {
		return nil, err
	}
	c.Log = logger

	c.AuthFile = "cookie/dell.cookie"

	c.Host = "https://7.3.20.16"
	c.WSHost = "wss://7.3.20.16"

	c.Username = DellAccount
	c.Password = DellPassword

	c.CrawlerData = new(DellCrawlerData)

	return c, nil
}

func (c *Dell) Start() {
	c.Log.Debug("抓取戴尔存储设备信息")

	// 验证授权信息
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

	// 启动WebSocket客户端
	if err := c.Connect(); err != nil {
		return
	}
	// 获取基础信息
	c.GetBasicInfo()
	// 获取硬盘信息、
	c.GetDiskInfo()

	c.CrawlerData.PrintStr()
}

func (c *Dell) Login() error {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}

	if login, err := client.Get(c.Host); err != nil {
		c.Log.Errorf("获取登录页面失败, error: %v", err)
		return err
	} else {
		defer func() {
			_ = login.Body.Close()
		}()
		authCookie := make([]string, 0)
		for _, cookie := range login.Cookies() {
			if cookie.Name == "DellStorageManagerSession" {
				cookieValue := strings.Replace(cookie.Value, "%2C%2C", "%2Czh_CN%2C", -1)
				authCookie = append(authCookie, cookie.Name+"="+cookieValue)
			}
		}
		c.AuthCookie = strings.Join(authCookie, ";")

		form := url.Values{
			"username":    []string{c.Username},
			"password":    []string{c.Password},
			"rememberMe":  []string{"on"},
			"authFailMsg": []string{"身份验证失败"},
		}

		loginUrl := c.Host + "/login"
		request, _ := http.NewRequest("POST", loginUrl, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Cookie", c.AuthCookie)

		// 发起请求
		if resp, err := client.Do(request); err != nil {
			c.Log.Errorf("登录失败, error: %v", err)
			return err
		} else {
			defer func() {
				_ = resp.Body.Close()
			}()

			if resp.StatusCode != 200 {
				c.Log.Errorf("登录失败, 错误码: %d", resp.StatusCode)
				return errors.New(fmt.Sprintf("登录失败, 错误码: %d", resp.StatusCode))
			}

			if err := ioutil.WriteFile(c.AuthFile, []byte(c.AuthCookie), os.ModePerm); err != nil {
				c.Log.Errorf("写入授权信息到文件失败, error: %v", err)
				return err
			}

			// 获取序列号
			ctxUrl := c.Host + "/session/context"
			ctxRequest, _ := http.NewRequest("GET", ctxUrl, nil)
			ctxRequest.Header.Set("Cookie", c.AuthCookie)
			if ctxResp, err := client.Do(ctxRequest); err != nil {
				c.Log.Errorf("获取系统上下文信息失败, error: %v", err)
				return err
			} else {
				defer func() {
					_ = ctxResp.Body.Close()
				}()
				if code := ctxResp.StatusCode; code != 200 {
					c.Log.Errorf("获取系统上下文信息失败, 错误码: %d", code)
					return errors.New(fmt.Sprintf("获取系统上下文信息失败, 错误码: %d", code))
				}

				if ctxBody, err := ioutil.ReadAll(ctxResp.Body); err != nil {
					c.Log.Errorf("读取系统上下文信息请求体数据失败, url: %s, error: %v", ctxUrl, err)
					return errors.New(fmt.Sprintf("读取系统上下文信息请求体数据失败, url: %s, error: %v", ctxUrl, err))
				} else {
					c.SerialNumber = gjson.Get(string(ctxBody), "pluginData.api.user.scSerialNumber").String()
				}
			}
			return nil
		}
	}
}

func (c *Dell) Connect() error {
	// 创建websocket客户端
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			RootCAs:            nil,
			InsecureSkipVerify: true,
		},
	}
	var err error
	c.wsConn, _, err = dialer.Dial(c.WSHost+"/messages", http.Header{
		"Cookie": []string{c.AuthCookie},
	})
	if err != nil || c.wsConn == nil {
		return err
	} else {
		return nil
	}
}

func (c *Dell) GetBasicInfo() {
	go func() {
		for {
			if _, message, err := c.wsConn.ReadMessage(); err != nil {
				c.Log.Errorf("Websocket读取数据失败, message: %b, error: %v", message, err)
				return
			} else {
				correlationId := gjson.Get(string(message), "correlationId").String()
				switch correlationId {
				case "1": // 总容量
					_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
						seriesColorId := gjson.Get(string(value), "seriesColorId").String()

						data := gjson.Get(string(value), "value").String()
						title := gjson.Get(string(value), "caption").String()
						if seriesColorId == "UsedSpace" { // 已使用
							c.CrawlerData.UsedSpace["data"] = data
							c.CrawlerData.UsedSpace["title"] = title
						} else if seriesColorId == "FreeSpace" { // 可用
							c.CrawlerData.FreeSpace["data"] = data
							c.CrawlerData.FreeSpace["title"] = title
						}
					}, "result.chartData")

					c.CrawlerData.TotalSpace["data"] = gjson.Get(string(message), "result.totalSpace.bytes").String()
					c.CrawlerData.TotalSpace["title"] = gjson.Get(string(message), "result.totalSpace.displayString").String()
				case "2": // 存储池容量
					_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
						seriesColorId := gjson.Get(string(value), "seriesColorId").String()

						data := gjson.Get(string(value), "value").String()
						title := gjson.Get(string(value), "caption").String()
						if seriesColorId == "UsedSpace" { // 已使用
							c.CrawlerData.PoolUsedSpace["data"] = data
							c.CrawlerData.PoolUsedSpace["title"] = title
						} else if seriesColorId == "FreeSpace" { // 可用
							c.CrawlerData.PoolFreeSpace["data"] = data
							c.CrawlerData.PoolFreeSpace["title"] = title
						}
					}, "result.sizeChartData")

					c.CrawlerData.PoolTotalSpace["data"] = gjson.Get(string(message), "result.allocatedSpace.bytes").String()
					c.CrawlerData.PoolTotalSpace["title"] = gjson.Get(string(message), "result.allocatedSpace.displayString").String()
				case "3": // 机柜状态
					_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
						enclosureInfo := make(map[string]string)
						enclosureInfo["name"] = gjson.Get(string(value), "name").String()
						enclosureInfo["index"] = gjson.Get(string(value), "index").String()
						enclosureInfo["instanceId"] = gjson.Get(string(value), "instanceId").String()
						enclosureInfo["status"] = gjson.Get(string(value), "status.enum").String()
						enclosureInfo["statusName"] = gjson.Get(string(value), "status.enumName").String()

						c.CrawlerData.EnclosureInfo = append(c.CrawlerData.EnclosureInfo, enclosureInfo)
					}, "result.enclosureList")
				}
			}
			c.wg.Done()
		}
	}()

	// 总容量
	c.Log.Debug("[RPC]总容量")
	getCapacityData := new(DellRpcRequest)
	getCapacityData.Type = "rpc-call"
	getCapacityData.PluginId = "sc"
	getCapacityData.CorrelationId = "1"
	getCapacityData.MethodName = "getCapacityData"
	getCapacityData.MethodArguments = []string{c.SerialNumber}
	getCapacityData.HandlerName = "StorageCenterSummaryService"
	getCapacityDataMsg, _ := json.Marshal(getCapacityData)
	if err := c.wsConn.WriteMessage(websocket.TextMessage, getCapacityDataMsg); err != nil {
		c.Log.Errorf("获取总容量请求发送失败, error: %v", err)
	} else {
		c.wg.Add(1)
	}

	// 存储池容量
	c.Log.Debug("[RPC]存储池容量")
	listStorageTypes := new(DellRpcRequest)
	listStorageTypes.Type = "rpc-call"
	listStorageTypes.PluginId = "sc"
	listStorageTypes.CorrelationId = "2"
	listStorageTypes.MethodName = "listStorageTypes"
	listStorageTypes.MethodArguments = []string{c.SerialNumber}
	listStorageTypes.HandlerName = "StorageTypeService"
	listStorageTypesMsg, _ := json.Marshal(listStorageTypes)
	if err := c.wsConn.WriteMessage(websocket.TextMessage, listStorageTypesMsg); err != nil {
		c.Log.Errorf("获取存储池容量请求发送失败, error: %v", err)
	} else {
		c.wg.Add(1)
	}

	// 机柜状态
	c.Log.Debug("[RPC]机柜状态")
	getHardwareOverview := new(DellRpcRequest)
	getHardwareOverview.Type = "rpc-call"
	getHardwareOverview.PluginId = "sc"
	getHardwareOverview.CorrelationId = "3"
	getHardwareOverview.MethodName = "getHardwareOverview"
	getHardwareOverview.MethodArguments = []string{c.SerialNumber}
	getHardwareOverview.HandlerName = "StorageCenterService"
	getHardwareOverviewMsg, _ := json.Marshal(getHardwareOverview)
	if err := c.wsConn.WriteMessage(websocket.TextMessage, getHardwareOverviewMsg); err != nil {
		c.Log.Errorf("获取机柜状态请求发送失败, error: %v", err)
	} else {
		c.wg.Add(1)
	}

	c.wg.Wait()
}

func (c *Dell) GetDiskInfo() {
	go func() {
		for {
			if _, message, err := c.wsConn.ReadMessage(); err != nil {
				c.Log.Errorf("Websocket读取数据失败, message: %b, error: %v", message, err)
				return
			} else {
				_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
					diskInfo := make(map[string]string)
					diskInfo["name"] = gjson.Get(string(value), "name").String()
					diskInfo["index"] = gjson.Get(string(value), "index").String()
					diskInfo["instanceId"] = gjson.Get(string(value), "instanceId").String()
					diskInfo["status"] = gjson.Get(string(value), "status.enum").String()
					diskInfo["statusName"] = gjson.Get(string(value), "status.enumName").String()

					c.CrawlerData.DiskInfo = append(c.CrawlerData.DiskInfo, diskInfo)
				}, "result.items")
			}
			c.wg.Done()
		}
	}()

	// 硬盘状态
	for i, info := range c.CrawlerData.EnclosureInfo {
		c.Log.Debug("[RPC]硬盘状态")
		getHardwareDisks := new(DellRpcRequest)
		getHardwareDisks.Type = "rpc-call"
		getHardwareDisks.PluginId = "sc"
		getHardwareDisks.CorrelationId = strconv.Itoa(4 + i)
		getHardwareDisks.MethodName = "getHardwareDisks"
		getHardwareDisks.MethodArguments = []string{c.SerialNumber, info["index"]}
		getHardwareDisks.HandlerName = "DiskService"
		getHardwareDisksMsg, _ := json.Marshal(getHardwareDisks)
		if err := c.wsConn.WriteMessage(websocket.TextMessage, getHardwareDisksMsg); err != nil {
			c.Log.Errorf("获取硬盘状态请求发送失败, error: %v", err)
		} else {
			c.wg.Add(1)
		}
	}

	c.wg.Wait()
}

func (c *Dell) GetPortInfo() {
	go func() {
		for {
			if _, message, err := c.wsConn.ReadMessage(); err != nil {
				c.Log.Errorf("Websocket读取数据失败, message: %b, error: %v", message, err)
				return
			} else {
				_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
					portInfo := make(map[string]string)
					portInfo["name"] = gjson.Get(string(value), "name").String()
					portInfo["instanceId"] = gjson.Get(string(value), "instanceId").String()
					portInfo["status"] = gjson.Get(string(value), "status.enum").String()
					portInfo["statusName"] = gjson.Get(string(value), "status.enumName").String()

					c.CrawlerData.PortInfo = append(c.CrawlerData.PortInfo, portInfo)
				}, "result.items")
			}
			c.wg.Done()
		}
	}()

	// 端口状态
	c.Log.Debug("[RPC]端口状态")
	getControllerPorts := new(DellRpcRequest)
	getControllerPorts.Type = "rpc-call"
	getControllerPorts.PluginId = "sc"
	getControllerPorts.CorrelationId = strconv.Itoa(4 + len(c.CrawlerData.EnclosureInfo))
	getControllerPorts.MethodName = "getControllerPorts"
	getControllerPorts.MethodArguments = []string{c.SerialNumber}
	getControllerPorts.HandlerName = "ControllerService"
	getControllerPortsMsg, _ := json.Marshal(getControllerPorts)
	if err := c.wsConn.WriteMessage(websocket.TextMessage, getControllerPortsMsg); err != nil {
		c.Log.Errorf("获取端口状态请求发送失败, error: %v", err)
	} else {
		c.wg.Add(1)
	}
}
