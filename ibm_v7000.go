package main

import (
	"bytes"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

var (
	//go:embed config/ibm_v7000_account
	IbmAccount string
	//go:embed config/ibm_v7000_password
	IbmPassword string
)

type IbmV7000CrawlerData struct {
}

func (h *IbmV7000CrawlerData) PrintFile(path string) {
	if data, err := json.Marshal(&h); err != nil {
		log.Fatalln(err)
	} else {
		_ = os.Remove(path)
		if err := ioutil.WriteFile(path, data, os.ModePerm); err != nil {
			log.Fatalln(err)
		}
	}
}

type IbmV7000 struct {
	Log *zap.SugaredLogger

	AuthFile   string
	AuthCookie string

	Host string

	Username string
	Password string

	CrawlerData *IbmV7000CrawlerData
}

func NewIbmV7000Crawler() (*IbmV7000, error) {
	c := new(IbmV7000)

	logger, err := NewLogger("ibm_v7000.log")
	if err != nil {
		return nil, err
	}
	c.Log = logger

	c.AuthFile = "cookie/ibm_v7000.cookie"

	c.Host = "https://7.3.20.15"

	c.Username = IbmAccount
	c.Password = IbmPassword

	c.CrawlerData = new(IbmV7000CrawlerData)

	return c, nil
}

func (c *IbmV7000) Debug() {

}

func (c *IbmV7000) Start() {
	c.Log.Debug("抓取IBM存储设备信息")

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
				c.Log.Debug("授权文件验证成功, 已跳过登陆")
				c.AuthCookie = string(cookie)
			} else {
				c.Log.Debug("授权信息文件为空, 需要执行登陆操作")
			}
		}
	} else {
		c.Log.Debug("未检查到授权信息文件, 需要执行登陆操作")
		if err := c.Login(); err != nil {
			c.Log.Errorf("登陆失败, 请重试, error: %v", err)
			return
		}
	}

	// 获取物理池状态
	if err := c.GetMonitorSystem(); err != nil {
		return
	}
	// 获取物理池状态
	if err := c.GetPhysicalPools(); err != nil {
		return
	}
	// 获取系统状态（实时）
	if err := c.GetClusterStates(); err != nil {
		return
	}
	// 获取节点状态（实时）
	if err := c.GetNodeStates(); err != nil {
		return
	}
	// 获取主机集群状态
	if err := c.GetHosts(); err != nil {
		return
	}
	// 获取内部存储器（磁盘）状态
	if err := c.GetPhysicalInternal(); err != nil {
		return
	}
	// 获取卷状态
	if err := c.GetVolumes(); err != nil {
		return
	}
}

func (c *IbmV7000) Login() error {
	c.Log.Debug("登陆用户获取授权信息")
	loginUrl := c.Host + "/login"

	// 构造请求客户端
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	// 请求登录页面获取JSESSIONID和_sync
	c.Log.Debug("获取登陆页面")
	if login, err := client.Get(loginUrl); err != nil {
		c.Log.Errorf("获取登录页面失败, error: %v", err)
		return err
	} else {
		defer func() {
			_ = login.Body.Close()
		}()
		cookie := make([]string, 0)
		cookies := login.Cookies()
		for i := 0; i < len(cookies); i++ {
			cookie = append(cookie, cookies[i].Name+"="+cookies[i].Value)
		}

		// 需要休息1秒, 否则会报错
		// too many request
		time.Sleep(1 * time.Second)

		c.Log.Debug("执行登陆请求")
		// 构造登录请求参数
		form := url.Values{
			"login":    []string{c.Username},
			"password": []string{c.Password},
			"tzoffset": []string{"-480"},
		}

		// 构造请求对象
		request, _ := http.NewRequest("POST", loginUrl, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Cookie", strings.Join(cookie, ";"))

		// 发起请求
		resp, err := client.Do(request)
		defer func() {
			_ = resp.Body.Close()
		}()
		if err != nil {
			c.Log.Errorf("登录失败, error: %v", err)
		}

		if resp.StatusCode != 200 {
			c.Log.Errorf("登录失败, 错误码: %d", resp.StatusCode)
			return errors.New(fmt.Sprintf("登录失败, 错误码: %d", resp.StatusCode))
		}

		c.Log.Debug("保存登陆之后的授权信息")
		authCookies := resp.Cookies()
		saveCookies := make([]string, 0)
		for i := 0; i < len(authCookies); i++ {
			cookie := authCookies[i]
			if cookie.Name == "_auth" || cookie.Name == "JSESSIONID" {
				saveCookies = append(saveCookies, cookie.Name+"="+cookie.Value)
			}
		}
		c.AuthCookie = strings.Join(saveCookies, ";")
		if err := ioutil.WriteFile(c.AuthFile, []byte(c.AuthCookie), os.ModePerm); err != nil {
			c.Log.Errorf("写入授权信息到文件失败, error: %v", err)
			return err
		}
		return nil
	}
}

func (c *IbmV7000) PostRPC(params io.Reader) (string, error) {
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	request, _ := http.NewRequest("POST", c.Host+"/RPCAdapter", params)
	request.Header.Set("Content-Type", "application/json-rpc")
	request.Header.Set("Cookie", c.AuthCookie)
	if resp, err := client.Do(request); err != nil {
		c.Log.Errorf("获取请求数据失败, params: %v, error: %v", params, err)
		return "", err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()
		if body, err := ioutil.ReadAll(resp.Body); err != nil {
			c.Log.Errorf("读取请求体数据失败, params: %v, error: %v", params, err)
			return "", err
		} else {
			return string(body), nil
		}
	}
}

func (c *IbmV7000) GetMonitorSystem() error {
	c.Log.Debug("[RPC]获取系统状态")

	// 请求参数
	params := map[string]interface{}{
		"clazz":       "com.ibm.evo.rpc.RPCRequest",
		"methodArgs":  []interface{}{},
		"methodClazz": "com.ibm.svc.gui.logic.ClusterRPC",
		"methodName":  "getClusterSystemBytes",
	}
	paramsJson, err := json.Marshal(params)
	if err != nil {
		c.Log.Errorf("JSON序列化出错, %v, error: %v", params, err)
		return err
	}

	if data, err := c.PostRPC(bytes.NewReader(paramsJson)); err != nil {
		c.Log.Errorf("[RPC]获取系统状态信息失败, error: %v", err)
		return err
	} else {
		c.Log.Debugf("[RPC]获取系统状态信息结果, %s", data)
		return nil
	}
}

func (c *IbmV7000) GetPhysicalPools() error {
	c.Log.Debug("[RPC]获取物理池状态")

	// 请求参数
	params := map[string]interface{}{
		"clazz":       "com.ibm.evo.rpc.RPCRequest",
		"methodArgs":  []interface{}{},
		"methodClazz": "com.ibm.svc.gui.logic.PoolsRPC",
		"methodName":  "getPools",
	}
	paramsJson, err := json.Marshal(params)
	if err != nil {
		c.Log.Errorf("JSON序列化出错, %v, error: %v", params, err)
		return err
	}

	if data, err := c.PostRPC(bytes.NewReader(paramsJson)); err != nil {
		c.Log.Errorf("[RPC]获取物理池状态失败, error: %v", err)
		return err
	} else {
		c.Log.Debugf("[RPC]获取物理池状态结果, %s", data)
		return nil
	}
}

func (c *IbmV7000) GetClusterStates() error {
	c.Log.Debug("[RPC]获取系统状态")

	// 请求参数
	params := map[string]interface{}{
		"clazz":       "com.ibm.evo.rpc.RPCRequest",
		"methodArgs":  []interface{}{},
		"methodClazz": "com.ibm.svc.gui.logic.ClusterRPC",
		"methodName":  "getClusterStats",
	}
	paramsJson, err := json.Marshal(params)
	if err != nil {
		c.Log.Errorf("JSON序列化出错, %v, error: %v", params, err)
		return err
	}

	if data, err := c.PostRPC(bytes.NewReader(paramsJson)); err != nil {
		c.Log.Errorf("[RPC]获取系统状态失败, error: %v", err)
		return err
	} else {
		c.Log.Debugf("[RPC]获取系统状态结果, %s", data)
		return nil
	}
}

func (c *IbmV7000) GetNodeStates() error {
	c.Log.Debug("[RPC]获取节点状态")

	// TODO: 获取节点数
	// 请求参数
	params := map[string]interface{}{
		"clazz":       "com.ibm.evo.rpc.RPCRequest",
		"methodArgs":  []int{1},
		"methodClazz": "com.ibm.svc.gui.logic.ClusterRPC",
		"methodName":  "getNodeStats",
	}
	paramsJson, err := json.Marshal(params)
	if err != nil {
		c.Log.Errorf("JSON序列化出错, %v, error: %v", params, err)
		return err
	}

	if data, err := c.PostRPC(bytes.NewReader(paramsJson)); err != nil {
		c.Log.Errorf("[RPC]获取节点状态失败, error: %v", err)
		return err
	} else {
		c.Log.Debugf("[RPC]获取节点状态结果, %s", data)
		return nil
	}
}

func (c *IbmV7000) GetHosts() error {
	c.Log.Debug("[RPC]获取主机集群状态")

	// 请求参数
	params := map[string]interface{}{
		"clazz":       "com.ibm.evo.rpc.RPCRequest",
		"methodArgs":  []interface{}{},
		"methodClazz": "com.ibm.svc.gui.logic.HostsRPC",
		"methodName":  "getHosts",
	}
	paramsJson, err := json.Marshal(params)
	if err != nil {
		c.Log.Errorf("JSON序列化出错, %v, error: %v", params, err)
		return err
	}

	if data, err := c.PostRPC(bytes.NewReader(paramsJson)); err != nil {
		c.Log.Errorf("[RPC]获取主机集群状态失败, error: %v", err)
		return err
	} else {
		c.Log.Debugf("[RPC]获取主机集群状态结果, %s", data)
		return nil
	}
}

func (c *IbmV7000) GetPhysicalInternal() error {
	c.Log.Debug("[RPC]获取内部存储器（磁盘）状态")

	// 请求参数
	params := map[string]interface{}{
		"clazz": "com.ibm.evo.rpc.RPCRequest",
		"guiUsage": []interface{}{
			map[string]interface{}{
				"event":     "Main GUI Panel Visited",
				"eventType": "pageVisited",
				"timestamp": "2021-09-16T15:33:39.633Z",
				"details": map[string]interface{}{
					"viewport": "Internal",
				},
			},
		},
		"methodArgs":  []interface{}{},
		"methodClazz": "com.ibm.svc.gui.logic.PhysicalRPC",
		"methodName":  "getInternalDriveInfo",
	}
	paramsJson, err := json.Marshal(params)
	if err != nil {
		c.Log.Errorf("JSON序列化出错, %v, error: %v", params, err)
		return err
	}

	if data, err := c.PostRPC(bytes.NewReader(paramsJson)); err != nil {
		c.Log.Errorf("[RPC]获取内部存储器（磁盘）状态失败, error: %v", err)
		return err
	} else {
		c.Log.Debugf("[RPC]获取内部存储器（磁盘）状态结果, %s", data)
		return nil
	}
}

func (c *IbmV7000) GetVolumes() error {
	c.Log.Debug("[POST]获取卷状态")

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}

	// TODO: 需要分页查询
	form := url.Values{
		"panelKey":          []string{"1631805210113"},
		"extendedMDiskInfo": []string{"false"},
		"password":          []string{"0"},
		"tzoffset":          []string{"40"},
	}
	request, _ := http.NewRequest("POST", c.Host+"/VDiskGridDataHandler", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Cookie", c.AuthCookie)
	if resp, err := client.Do(request); err != nil {
		c.Log.Errorf("获取请求数据失败, params: %v, error: %v", form, err)
		return err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()
		if body, err := ioutil.ReadAll(resp.Body); err != nil {
			c.Log.Errorf("读取请求体数据失败, params: VDiskGridDataHandler, error: %v", err)
			return err
		} else {
			fmt.Println(string(body))
			return nil
		}
	}
}
