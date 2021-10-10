package main

import (
	_ "embed"

	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
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

	c.GetSystemInfo()
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

		code := resp.StatusCode
		if code != 200 {
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
		element := doc.FindElement("//RESPONSE/OBJECT/PROPERTY[@name='response']")
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
				code := localResp.StatusCode
				if code != 200 {
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
		c.Log.Errorf("请求失败, url: %v, error: %v", url, err)
		return "", err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()

		// 判断HTTP状态码
		code := resp.StatusCode
		if code != 200 {
			c.Log.Errorf("请求失败, url, %s, 错误码: %d, 错误信息: %v", url, code, resp.Header)
			return "", errors.New("请求失败")
		}
		if body, err := ioutil.ReadAll(resp.Body); err != nil {
			c.Log.Errorf("读取请求体数据失败, url: %s, error: %v", url, err)
			return "", err
		} else {
			return string(body), nil
		}
	}
}

func (c *HP) GetSystemInfo() {
	c.Log.Debug("[REST]系统信息")

	requestUrl := fmt.Sprintf("%s/v3/api/show/system?_=%d", c.Host, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求系统信息失败, error: %v", err)
	} else {
		fmt.Println(data)
	}
}
