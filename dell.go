package main

import (
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/buger/jsonparser"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
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

	PoolInfo      []map[string]string `json:"poolInfo"`      // 存储池信息
	EnclosureInfo []map[string]string `json:"enclosureInfo"` // 机柜信息
	DiskInfo      []map[string]string `json:"diskInfo"`      // 磁盘信息
	PortInfo      []map[string]string `json:"portInfo"`      // 端口信息

	VolumeState []map[string]int64 `json:"volumeState"` // 卷状态
	DiskState   []map[string]int64 `json:"diskState"`   // 磁盘
	PortState   []map[string]int64 `json:"portState"`   // 端口状态
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

	c.CrawlerData.UsedSpace = make(map[string]string)
	c.CrawlerData.FreeSpace = make(map[string]string)
	c.CrawlerData.TotalSpace = make(map[string]string)
	return c, nil
}

func (c *Dell) Start() {
	c.Log.Debug("抓取戴尔存储设备信息")

	if err := c.preStart(); err != nil {
		return
	}

	// 获取基础信息
	if err := c.GetBasicInfo(); err != nil {
		return
	}
	// 获取硬盘信息
	if err := c.GetDiskInfo(); err != nil {
		return
	}
	// 获取端口信息
	if err := c.GetPortInfo(); err != nil {
		return
	}

	// 获取系统指标
	if err := c.GetSystemStatus(); err != nil {
		return
	}
	c.CrawlerData.PrintStr()
}

func (c *Dell) preStart() error {
	// 验证授权信息
	if isExist(c.AuthFile) {
		c.Log.Debug("检查到授权信息文件")
		if cookie, err := ioutil.ReadFile(c.AuthFile); err != nil {
			c.Log.Errorf("读取授权信息文件失败, 需要重新登陆, error: %v", err)
			if err := c.Login(); err != nil {
				c.Log.Errorf("登陆失败, 请重试, error: %v", err)
				return err
			}
		} else {
			// 需要判断授权是否过期
			if len(cookie) > 0 {
				c.AuthCookie = string(cookie)
			} else {
				c.Log.Debug("授权信息文件为空, 执行登陆操作")
				if err := c.Login(); err != nil {
					c.Log.Errorf("登陆失败, 请重试, error: %v", err)
					return err
				}
			}
		}
	} else {
		c.Log.Debug("未检查到授权信息文件, 执行登陆操作")
		if err := c.Login(); err != nil {
			c.Log.Errorf("登陆失败, 请重试, error: %v", err)
			return err
		}
	}

	// 获取系统信息
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
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
			if code == 401 {
				_ = os.Remove(c.AuthFile)
				c.Log.Errorf("权限验证失败, 移除cookie文件, 请重新运行")
				return errors.New("权限验证失败")
			} else {
				c.Log.Errorf("获取系统上下文信息失败, 错误码: %d", code)
				return errors.New(fmt.Sprintf("获取系统上下文信息失败, 错误码: %d", code))
			}
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

func (c *Dell) Login() error {
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

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
			return nil
		}
	}
}

func (c *Dell) GetBasicInfo() error {
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			RootCAs:            nil,
			InsecureSkipVerify: true,
		},
	}
	wsConn, _, err := dialer.Dial(c.WSHost+"/messages", http.Header{
		"Cookie": []string{c.AuthCookie},
	})
	if err != nil || wsConn == nil {
		return err
	}

	var wg sync.WaitGroup
	go func() {
		for {
			if _, message, err := wsConn.ReadMessage(); err != nil {
				c.Log.Errorf("Websocket读取数据失败, message: %b, error: %v", message, err)
				wg.Done()
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
					}, "result", "chartData")

					c.CrawlerData.TotalSpace["data"] = gjson.Get(string(message), "result.totalSpace.bytes").String()
					c.CrawlerData.TotalSpace["title"] = gjson.Get(string(message), "result.totalSpace.displayString").String()
				case "2": // 存储池容量
					_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
						poolInfo := make(map[string]string)
						_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
							seriesColorId := gjson.Get(string(value), "seriesColorId").String()

							data := gjson.Get(string(value), "value").String()
							title := gjson.Get(string(value), "caption").String()
							if seriesColorId == "UsedSpace" { // 已使用
								poolInfo["usedSpaceData"] = data
								poolInfo["usedSpaceTitle"] = title
							} else if seriesColorId == "FreeSpace" { // 可用
								poolInfo["freeSpaceData"] = data
								poolInfo["freeSpaceTitle"] = title
							}
						}, "sizeChartData")

						poolInfo["totalSpaceData"] = gjson.Get(string(value), "allocatedSpace.bytes").String()
						poolInfo["totalSpaceTitle"] = gjson.Get(string(value), "allocatedSpace.displayString").String()
					}, "result")
				case "3": // 机柜状态
					_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
						enclosureInfo := make(map[string]string)
						enclosureInfo["name"] = gjson.Get(string(value), "name").String()
						enclosureInfo["index"] = gjson.Get(string(value), "index").String()
						enclosureInfo["instanceId"] = gjson.Get(string(value), "instanceId").String()
						enclosureInfo["status"] = gjson.Get(string(value), "status.enum").String()
						enclosureInfo["statusName"] = gjson.Get(string(value), "status.enumName").String()

						c.CrawlerData.EnclosureInfo = append(c.CrawlerData.EnclosureInfo, enclosureInfo)
					}, "result", "enclosureList")
				}
			}
			wg.Done()
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
	if err := wsConn.WriteMessage(websocket.TextMessage, getCapacityDataMsg); err != nil {
		c.Log.Errorf("获取总容量请求发送失败, error: %v", err)
	} else {
		wg.Add(1)
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
	if err := wsConn.WriteMessage(websocket.TextMessage, listStorageTypesMsg); err != nil {
		c.Log.Errorf("获取存储池容量请求发送失败, error: %v", err)
	} else {
		wg.Add(1)
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
	if err := wsConn.WriteMessage(websocket.TextMessage, getHardwareOverviewMsg); err != nil {
		c.Log.Errorf("获取机柜状态请求发送失败, error: %v", err)
	} else {
		wg.Add(1)
	}

	wg.Wait()
	return nil
}

func (c *Dell) GetDiskInfo() error {
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			RootCAs:            nil,
			InsecureSkipVerify: true,
		},
	}
	wsConn, _, err := dialer.Dial(c.WSHost+"/messages", http.Header{
		"Cookie": []string{c.AuthCookie},
	})
	if err != nil || wsConn == nil {
		return err
	}

	var wg sync.WaitGroup
	go func() {
		for {
			if _, message, err := wsConn.ReadMessage(); err != nil {
				c.Log.Errorf("Websocket读取数据失败, message: %b, error: %v", message, err)
				wg.Done()
			} else {
				_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
					diskInfo := make(map[string]string)
					diskInfo["name"] = gjson.Get(string(value), "name").String()
					diskInfo["index"] = gjson.Get(string(value), "index").String()
					diskInfo["instanceId"] = gjson.Get(string(value), "instanceId").String()
					diskInfo["status"] = gjson.Get(string(value), "status.enum").String()
					diskInfo["statusName"] = gjson.Get(string(value), "status.enumName").String()

					c.CrawlerData.DiskInfo = append(c.CrawlerData.DiskInfo, diskInfo)
				}, "result", "items")
			}
			wg.Done()
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
		if err := wsConn.WriteMessage(websocket.TextMessage, getHardwareDisksMsg); err != nil {
			c.Log.Errorf("获取硬盘状态请求发送失败, error: %v", err)
		} else {
			wg.Add(1)
		}
	}

	wg.Wait()
	return nil
}

func (c *Dell) GetPortInfo() error {
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			RootCAs:            nil,
			InsecureSkipVerify: true,
		},
	}
	wsConn, _, err := dialer.Dial(c.WSHost+"/messages", http.Header{
		"Cookie": []string{c.AuthCookie},
	})
	if err != nil || wsConn == nil {
		return err
	}

	var wg sync.WaitGroup
	go func() {
		for {
			if _, message, err := wsConn.ReadMessage(); err != nil {
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
				}, "result", "items")
			}
			wg.Done()
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
	if err := wsConn.WriteMessage(websocket.TextMessage, getControllerPortsMsg); err != nil {
		c.Log.Errorf("获取端口状态请求发送失败, error: %v", err)
	} else {
		wg.Add(1)
	}

	wg.Wait()
	return nil
}

func (c *Dell) GetSystemStatus() error {
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			RootCAs:            nil,
			InsecureSkipVerify: true,
		},
	}
	wsConn, _, err := dialer.Dial(c.WSHost+"/messages", http.Header{
		"Cookie": []string{c.AuthCookie},
	})
	if err != nil || wsConn == nil {
		return err
	}

	var wg sync.WaitGroup
	go func() {
		for {
			if _, message, err := wsConn.ReadMessage(); err != nil {
				c.Log.Errorf("Websocket读取数据失败, message: %b, error: %v", message, err)
				return
			} else {
				// 对应关系
				// SYSTEM = ["StorageCenter", "ScVolume", "ScDisk"],
				// SERVERS = ["ScServerFolder", "ScPhysicalServer", "ScServer", "ScServerCluster", "ScVirtualServer", "ScServerHba"],
				// FAULTDOMAINS = ["ScFaultDomainFolder", "ScFaultDomain", "ScIscsiFaultDomain", "ScFibreChannelFaultDomain", "ScSasFaultDomain"],
				// CONTROLLERS = ["ScControllerFolder", "ScController", "ScControllerPortType", "ScControllerPort"],
				// DISKS = ["ScDiskFolder", "ScDiskFolderClass", "ScDisk", "ScDiskClass"],
				// VOLUMES = ["ScVolumeFolder", "ScVolume"],
				// STORAGEPROFILES = ["ScStorageProfileFolder", "ScStorageProfile"],
				// QOSPROFILES = ["ScQosProfileFolder", "ScQosProfileType", "ScQosProfile"],

				// 函数调用堆栈
				// RealTimeChartSource.prototype.getChartData -> filterData -> filterSlices
				// getAverageDataValues -> calcAvgOverTime -> calcAvgOverTime -> calAvgAndUpdateUsageValues

				// 系统对应处理函数 -> updateIoUsageForSystem
				// 其他对应处理函数 -> updateIoUsage

				_, _ = jsonparser.ArrayEach(message, func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
					state := make(map[string]int64)

					objType := gjson.Get(string(value), "objType").String()
					_ = json.Unmarshal([]byte(gjson.Get(string(value), "values").String()), &state)
					switch objType {
					case "ScVolume":
						c.CrawlerData.VolumeState = append(c.CrawlerData.VolumeState, state)
					case "ScDisk":
						c.CrawlerData.DiskState = append(c.CrawlerData.DiskState, state)
					case "ScFibreChannelFaultDomain":
						c.CrawlerData.PortState = append(c.CrawlerData.PortState, state)
					}
				}, "result", "data")
			}
			wg.Done()
		}
	}()

	// 系统实时状态
	c.Log.Debug("[RPC]获取系统实时状态")
	gatherStatsInformation := new(DellRpcRequest)
	gatherStatsInformation.Type = "rpc-call"
	gatherStatsInformation.PluginId = "sc"
	gatherStatsInformation.MethodName = "gatherStatsInformation"
	gatherStatsInformation.MethodArguments = []string{
		time.Now().Format("2006-01-02T15:04:05.000Z"),
		c.SerialNumber,
	}
	gatherStatsInformation.HandlerName = "RealTimeDataService"
	gatherStatsInformationMsg, _ := json.Marshal(gatherStatsInformation)
	if err := wsConn.WriteMessage(websocket.TextMessage, gatherStatsInformationMsg); err != nil {
		c.Log.Errorf("获取获取系统实时状态请求发送失败, error: %v", err)
	} else {
		wg.Add(1)
	}

	wg.Wait()
	return nil
}
