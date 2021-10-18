package main

import (
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
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

	fmt.Println(c.AuthCookie)
	c.Connect()
	// 启动WebSocket客户端
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

func (c *Dell) Connect() {
	// 创建websocket客户端
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
		return
	}

	// 同步等待所有异步任务结束
	var wg sync.WaitGroup

	go func() {
		for {
			_, message, err := wsConn.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Printf("recv: %s", string(message))
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

	// 硬盘状态
	c.Log.Debug("[RPC]硬盘状态")
	getHardwareDisks := new(DellRpcRequest)
	getHardwareDisks.Type = "rpc-call"
	getHardwareDisks.PluginId = "sc"
	getHardwareDisks.CorrelationId = "3"
	getHardwareDisks.MethodName = "getHardwareDisks"
	getHardwareDisks.MethodArguments = []string{c.SerialNumber}
	getHardwareDisks.HandlerName = "DiskService"
	getHardwareDisksMsg, _ := json.Marshal(getHardwareDisks)
	if err := wsConn.WriteMessage(websocket.TextMessage, getHardwareDisksMsg); err != nil {
		c.Log.Errorf("获取硬盘状态请求发送失败, error: %v", err)
	} else {
		wg.Add(1)
	}

	wg.Wait()
}
