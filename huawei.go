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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

var (
	//go:embed huawei_enum.json
	HuaweiEnumDefine string
)

var (
	//go:embed config/huawei_account
	HuaweiAccount string
	//go:embed config/huawei_password
	HuaweiPassword string
)

type HuaweiCrawlerData struct {
	SectorSize int64 `json:"sectorSize"`

	UsableDiskpoolCapacityData int64 `json:"usableDiskpoolCapacityData"`

	ServerStatus            string `json:"serverStatus"`
	ServerStatusDescription string `json:"serverStatusDescription"`
	ProductMode             string `json:"productMode"`
	SystemCapacity          int64  `json:"systemCapacity"`
	SystemUsedCapacity      int64  `json:"systemUsedCapacity"`
	LunCapacity             int64  `json:"lunCapacity"`
	FilesystemCapacity      int64  `json:"filesystemCapacity"`
	DataProtectCapacity     int64  `json:"dataProtectCapacity"`
	FreePoolCapacity        int64  `json:"freePoolCapacity"`
	UsableCapacity          int64  `json:"usableCapacity"`
	TotalCapacity           int64  `json:"totalCapacity"`

	StoragePoolInfo []interface{} `json:"storagePoolInfo"`
	FanInfo         []interface{} `json:"fanInfo"`
	PowerInfo       []interface{} `json:"powerInfo"`
	FcPortInfo      []interface{} `json:"fcPortInfo"`
}

func (h *HuaweiCrawlerData) PrintFile(path string) {
	if data, err := json.Marshal(&h); err != nil {
		log.Fatalln(err)
	} else {
		_ = os.Remove(path)
		if err := ioutil.WriteFile(path, data, os.ModePerm); err != nil {
			log.Fatalln(err)
		}
	}
}

type Huawei struct {
	Log *zap.SugaredLogger

	AuthFile   string
	AuthCookie string

	Host     string
	Username string
	Password string

	DeviceId string

	CrawlerData *HuaweiCrawlerData
}

func NewHuaweiCrawler() (*Huawei, error) {
	c := new(Huawei)

	logger, err := NewLogger("huawei.log")
	if err != nil {
		return nil, err
	}
	c.Log = logger

	c.AuthFile = "cookie/huawei.cookie"

	c.Host = "https://7.3.20.34:8088"
	c.Username = HuaweiAccount
	c.Password = HuaweiPassword

	c.CrawlerData = new(HuaweiCrawlerData)

	return c, nil
}

func (c *Huawei) Start() {
	c.Log.Debug("开始抓取Huawei存储设备")

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

	// 数据初始化
	c.CrawlerData.SectorSize = 512
	c.CrawlerData.StoragePoolInfo = make([]interface{}, 0)
	c.CrawlerData.FanInfo = make([]interface{}, 0)
	c.CrawlerData.PowerInfo = make([]interface{}, 0)

	// 服务状态
	// c.GetServerStatus()
	// 系统基本信息
	// c.GetSystemInfo()
	// 存储池信息
	// c.GetStoragePoolInfo()
	// 风扇信息
	// c.GetFanInfo()
	// 电源信息
	// c.GetPowerInfo()
	// FC端口信息
	// c.GetFcPortInfo()

	c.CrawlerData.PrintFile("huawei_text.txt")

	// 当前系统各种参数的实时指标状态
	c.GetCurrentState()
}

func (c *Huawei) Login() error {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}

	params := map[string]interface{}{
		"scope":     0,
		"username":  c.Username,
		"password":  c.Password,
		"isEncrypt": true,
		"loginMode": 3,
	}

	paramsJson, err := json.Marshal(params)
	if err != nil {
		c.Log.Errorf("JSON序列化出错, %v, error: %v", params, err)
		return err
	}

	loginUrl := c.Host + "/deviceManager/rest/xxxxx/login"
	request, _ := http.NewRequest("POST", loginUrl, bytes.NewReader(paramsJson))
	request.Header.Set("Content-Type", "application/json;charset=UTF-8")
	request.Header.Set("Cookie", c.AuthCookie)

	if resp, err := client.Do(request); err != nil {
		c.Log.Errorf("请求失败, params: %v, error: %v", params, err)
		return err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()

		cookies := resp.Cookies()
		if len(cookies) > 0 {
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				c.Log.Errorf("读取请求体数据失败, params: %v, error: %v", params, err)
				return err
			}

			c.DeviceId = gjson.Get(string(body), "data.deviceid").String()
			c.AuthCookie = cookies[0].Name + "=" + cookies[0].Value
			if err := os.MkdirAll(filepath.Dir(c.AuthFile), os.ModePerm); err != nil {
				c.Log.Errorf("创建授权信息文件失败, error: %v", err)
				return err
			}
			if err := ioutil.WriteFile(c.AuthFile, []byte(c.AuthCookie), os.ModePerm); err != nil {
				c.Log.Errorf("写入授权信息到文件失败, error: %v", err)
				return err
			}
			return nil
		} else {
			return errors.New("登陆失败，未获取到cookie")
		}
	}
}

func (c *Huawei) RequestJson(method, url string, params io.Reader) (string, error) {
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
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", c.AuthCookie)

	if resp, err := client.Do(request); err != nil {
		c.Log.Errorf("数据失败, url: %v, error: %v", url, err)
		return "", err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()

		// 判断HTTP状态码
		code := resp.StatusCode
		if code != 200 {
			c.Log.Errorf("请求错误, url, %s, 错误码: %d, 错误信息: %v", url, code, resp.Header)
			return "", errors.New("请求错误")
		}

		if body, err := ioutil.ReadAll(resp.Body); err != nil {
			c.Log.Errorf("读取请求体数据失败, url: %s, error: %v", url, err)
			return "", err
		} else {
			// 判断业务状态码
			errorCode := gjson.Get(string(body), "error.code").String()
			if errorCode != "0" {
				if errorCode == "-401" {
					_ = os.Remove(c.AuthFile)
					c.Log.Errorf("权限验证失败, 移除cookie文件, 请重新运行")
				} else {
					c.Log.Errorf("请求错误, url: %s, 错误码: %s, 错误信息: %s",
						url, errorCode, gjson.Get(string(body), "error.description").String())
				}
				return "", errors.New("请求错误")
			} else {
				return string(body), nil
			}
		}
	}
}

func (c *Huawei) GetServerStatus() {
	c.Log.Debug("[REST]服务状态")

	getUrl := fmt.Sprintf("%s/deviceManager/rest/%s/server/status?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", getUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求服务状态失败, error: %v", err)
	} else {
		// 服务状态
		status := gjson.Get(data, "data.status").String()
		c.CrawlerData.ServerStatus = status

		// 服务状态描述
		description := gjson.Get(data, "data.description").String()
		c.CrawlerData.ServerStatusDescription = description
	}
}

func (c *Huawei) GetSystemInfo() {
	c.Log.Debug("[REST]系统信息")

	getUrl := fmt.Sprintf("%s/deviceManager/rest/%s/system/?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", getUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求系统信息失败, error: %v", err)
	} else {
		// 设备型号
		productMode := gjson.Get(data, "data.PRODUCTMODE").String()
		productModeEnum := gjson.Get(HuaweiEnumDefine, "PRODUCT_MODE_E").Map()
		for k, v := range productModeEnum {
			if v.String() == productMode {
				c.CrawlerData.ProductMode = k
			}
		}

		// 硬盘域信息
		c.GetDiskPoolForSystem()
		usableDiskpoolCapacityData := c.CrawlerData.UsableDiskpoolCapacityData
		// 扇区大小
		sectorSize := gjson.Get(data, "data.SECTORSIZE").Int()
		c.CrawlerData.SectorSize = sectorSize
		usedRawCapacity := gjson.Get(data, "data.MEMBERDISKSCAPACITY").Int()
		unusedRawCapacity := gjson.Get(data, "data.FREEDISKSCAPACITY").Int()
		usedCapacity := usedRawCapacity - usableDiskpoolCapacityData
		unusedCapacity := unusedRawCapacity + usableDiskpoolCapacityData

		// 系统容量（Byte）
		c.CrawlerData.SystemCapacity = (usedCapacity + unusedCapacity) * sectorSize

		// 系统已使用容量（Byte）
		c.CrawlerData.SystemUsedCapacity = usedCapacity * sectorSize

		// 存储池信息
		c.GetStoragePoolForSystem()
		// LUN
		lunCapacity := c.CrawlerData.LunCapacity
		c.CrawlerData.LunCapacity = lunCapacity * sectorSize

		// 文件系统
		filesystemCapacity := c.CrawlerData.FilesystemCapacity
		c.CrawlerData.FilesystemCapacity = filesystemCapacity * sectorSize

		// 数据保护
		dataProtectCapacity := c.CrawlerData.DataProtectCapacity
		c.CrawlerData.DataProtectCapacity = dataProtectCapacity * sectorSize

		// 空闲容量
		freePoolCapacity := c.CrawlerData.FreePoolCapacity
		c.CrawlerData.FreePoolCapacity = freePoolCapacity * sectorSize

		// 总可用容量
		usableCapacity := lunCapacity + filesystemCapacity + dataProtectCapacity + freePoolCapacity
		c.CrawlerData.UsableCapacity = usableCapacity * sectorSize

		// 总订阅容量
		// c.CrawlerData.TotalCapacity
	}
}

func (c *Huawei) GetDiskPoolForSystem() {
	c.Log.Debug("[REST]硬盘域信息")

	getUrl := fmt.Sprintf("%s/deviceManager/rest/%s/diskpool?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", getUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求硬盘域信息失败, error: %v", err)
	} else {
		// 解析数据
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			// 保存数据
			c.CrawlerData.UsableDiskpoolCapacityData = c.CrawlerData.UsableDiskpoolCapacityData + gjson.Get(string(value), "FREECAPACITY").Int()
		}, "data")
	}
}

func (c *Huawei) GetStoragePoolForSystem() {
	c.Log.Debug("[REST]存储池信息(For 系统信息)")

	getUrl := fmt.Sprintf("%s/deviceManager/rest/%s/storagepool?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", getUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求存储池信息(For 系统信息)失败, error: %v", err)
	} else {
		// 解析数据
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			// 用户消耗容量
			userConsumedCapacity := gjson.Get(string(value), "USERCONSUMEDCAPACITY").Int()
			usageType := gjson.Get(string(value), "USAGETYPE").String()
			if usageType == "1" {
				// LUN
				c.CrawlerData.LunCapacity = c.CrawlerData.LunCapacity + userConsumedCapacity
				// 总订阅容量
				c.CrawlerData.TotalCapacity = c.CrawlerData.TotalCapacity + gjson.Get(string(value), "LUNCONFIGEDCAPACITY").Int()
			} else if usageType == "2" {
				// 文件系统
				c.CrawlerData.FilesystemCapacity = c.CrawlerData.FilesystemCapacity + userConsumedCapacity
				// 总订阅容量
				c.CrawlerData.TotalCapacity = c.CrawlerData.TotalCapacity + gjson.Get(string(value), "TOTALFSCAPACITY").Int()
			}

			// 数据保护
			c.CrawlerData.DataProtectCapacity = c.CrawlerData.DataProtectCapacity + gjson.Get(string(value), "REPLICATIONCAPACITY").Int()

			// 空闲容量
			c.CrawlerData.FreePoolCapacity = c.CrawlerData.FreePoolCapacity + gjson.Get(string(value), "USERFREECAPACITY").Int()
		}, "data")
	}
}

func (c *Huawei) GetStoragePoolInfo() {
	c.Log.Debug("[REST]存储池信息")

	getUrl := fmt.Sprintf("%s/deviceManager/rest/%s/storagepool?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", getUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求存储池信息失败, error: %v", err)
	} else {
		// 解析数据
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			// 存储池信息
			storagePoolInfo := make(map[string]interface{})

			storagePoolInfo["id"] = gjson.Get(string(value), "ID").String()
			storagePoolInfo["name"] = gjson.Get(string(value), "NAME").String()

			// 健康状态
			healthStatus := gjson.Get(string(value), "HEALTHSTATUS").String()
			healthStatusEnum := gjson.Get(HuaweiEnumDefine, "HEALTH_STATUS_E").Map()
			for k, v := range healthStatusEnum {
				if v.String() == healthStatus {
					storagePoolInfo["healthStatus"] = k
				}
			}

			// 运行状态
			runningStatus := gjson.Get(string(value), "RUNNINGSTATUS").String()
			runningStatusEnum := gjson.Get(HuaweiEnumDefine, "RUNNING_STATUS_E").Map()
			for k, v := range runningStatusEnum {
				if v.String() == runningStatus {
					storagePoolInfo["runningStatus"] = k
				}
			}

			// 空闲容量
			userFreeCapacity := gjson.Get(string(value), "USERFREECAPACITY").Int()
			storagePoolInfo["userFreeCapacity"] = userFreeCapacity * c.CrawlerData.SectorSize

			// 总容量
			userTotalCapacity := gjson.Get(string(value), "USERTOTALCAPACITY").Int()
			storagePoolInfo["userTotalCapacity"] = userTotalCapacity * c.CrawlerData.SectorSize

			c.CrawlerData.StoragePoolInfo = append(c.CrawlerData.StoragePoolInfo, storagePoolInfo)
		}, "data")
	}
}

func (c *Huawei) GetFanInfo() {
	c.Log.Debug("[REST]风扇信息")

	getUrl := fmt.Sprintf("%s/deviceManager/rest/%s/fan?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", getUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求风扇信息失败, error: %v", err)
	} else {
		// 解析数据
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			fanInfo := make(map[string]interface{})

			fanInfo["id"] = gjson.Get(string(value), "ID").String()
			fanInfo["name"] = gjson.Get(string(value), "NAME").String()

			fanInfo["location"] = gjson.Get(string(value), "LOCATION").String()

			// 健康状态
			healthStatus := gjson.Get(string(value), "HEALTHSTATUS").String()
			healthStatusEnum := gjson.Get(HuaweiEnumDefine, "HEALTH_STATUS_E").Map()
			for k, v := range healthStatusEnum {
				if v.String() == healthStatus {
					fanInfo["healthStatus"] = k
				}
			}

			// 运行状态
			runningStatus := gjson.Get(string(value), "RUNNINGSTATUS").String()
			runningStatusEnum := gjson.Get(HuaweiEnumDefine, "RUNNING_STATUS_E").Map()
			for k, v := range runningStatusEnum {
				if v.String() == runningStatus {
					fanInfo["runningStatus"] = k
				}
			}

			c.CrawlerData.FanInfo = append(c.CrawlerData.FanInfo, fanInfo)
		}, "data")
	}
}

func (c *Huawei) GetPowerInfo() {
	c.Log.Debug("[REST]电源信息")

	getUrl := fmt.Sprintf("%s/deviceManager/rest/%s/power?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", getUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求电源信息失败, error: %v", err)
	} else {
		// 解析数据
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			powerInfo := make(map[string]interface{})

			powerInfo["id"] = gjson.Get(string(value), "ID").String()
			powerInfo["name"] = gjson.Get(string(value), "NAME").String()

			// 健康状态
			healthStatus := gjson.Get(string(value), "HEALTHSTATUS").String()
			healthStatusEnum := gjson.Get(HuaweiEnumDefine, "HEALTH_STATUS_E").Map()
			for k, v := range healthStatusEnum {
				if v.String() == healthStatus {
					powerInfo["healthStatus"] = k
				}
			}

			// 运行状态
			runningStatus := gjson.Get(string(value), "RUNNINGSTATUS").String()
			runningStatusEnum := gjson.Get(HuaweiEnumDefine, "RUNNING_STATUS_E").Map()
			for k, v := range runningStatusEnum {
				if v.String() == runningStatus {
					powerInfo["runningStatus"] = k
				}
			}

			c.CrawlerData.PowerInfo = append(c.CrawlerData.PowerInfo, powerInfo)
		}, "data")
	}
}

func (c *Huawei) GetFcPortInfo() {
	c.Log.Debug("[REST]FC端口信息")

	getUrl := fmt.Sprintf("%s/deviceManager/rest/%s/fc_port?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", getUrl, nil); err != nil {
		c.Log.Errorf("[REST]请求FC端口信息失败, error: %v", err)
	} else {
		// 解析数据
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			fcPortInfo := make(map[string]interface{})

			fcPortInfo["id"] = gjson.Get(string(value), "ID").String()
			fcPortInfo["name"] = gjson.Get(string(value), "NAME").String()

			// 健康状态
			healthStatus := gjson.Get(string(value), "HEALTHSTATUS").String()
			healthStatusEnum := gjson.Get(HuaweiEnumDefine, "HEALTH_STATUS_E").Map()
			for k, v := range healthStatusEnum {
				if v.String() == healthStatus {
					fcPortInfo["healthStatus"] = k
				}
			}

			// 运行状态
			runningStatus := gjson.Get(string(value), "RUNNINGSTATUS").String()
			runningStatusEnum := gjson.Get(HuaweiEnumDefine, "RUNNING_STATUS_E").Map()
			for k, v := range runningStatusEnum {
				if v.String() == runningStatus {
					fcPortInfo["runningStatus"] = k
				}
			}

			c.CrawlerData.FcPortInfo = append(c.CrawlerData.FcPortInfo, fcPortInfo)
		}, "data")
	}
}

func (c *Huawei) GetCurrentState() {
	// 采集指标
	indexList := []string{"fc_port", "disk", "diskpool", "lun"}

	// 总IOPS,读IOPS,写IOPS,最大IOPS,读带宽,写带宽
	baseDataIdList := "22,25,28,307,23,26"
	baseUrl := fmt.Sprintf("%s/deviceManager/rest/%s/performace_statistic/cur_statistic_data", c.Host, c.DeviceId)
	for i := 0; i < len(indexList); i++ {
		index := indexList[i]

		// 查询列表
		uuidList := make([]string, 0)
		listUrl := fmt.Sprintf("%s/deviceManager/rest/%s/%s?t=%d", c.Host, c.DeviceId, index, time.Now().UnixNano()/1e6)
		if data, err := c.RequestJson("GET", listUrl, nil); err != nil {
			c.Log.Errorf("[REST]请求[%s]列表数据失败, error: %v", index, err)
			return
		} else {
			_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
				dataType := gjson.Get(string(value), "TYPE").String()
				dataId := gjson.Get(string(value), "ID").String()

				uuidList = append(uuidList, dataType+":"+dataId)
			}, "data")
		}

		// 指标信息
		for i := 0; i < len(uuidList); i++ {
			var (
				uuid       = uuidList[i]
				dataIdList = baseDataIdList
			)

			if index == "diskpool" {
				dataIdList = strings.Replace(baseDataIdList, "307,", "", 1)
			}

			collectUrl := fmt.Sprintf("%s?CMO_STATISTIC_UUID=%s&CMO_STATISTIC_DATA_ID_LIST=%s&timeConversion=1", baseUrl, uuid, dataIdList)

			if data, err := c.RequestJson("GET", collectUrl, nil); err != nil {
				c.Log.Errorf("[REST]请求[%s]指标信息失败, error: %v", index, err)
				return
			} else {
				c.Log.Debugf("[REST]UUID[%s]的[%s]的指标项数据, %s", index, uuid, gjson.Get(data, "data.0"))
			}
		}
	}
}
