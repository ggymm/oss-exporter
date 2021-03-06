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
	data, _ := json.Marshal(&h)
	_ = os.Remove(path)
	if err := ioutil.WriteFile(path, data, os.ModePerm); err != nil {
		log.Fatalln(err)
	}
}

type Huawei struct {
	Log *zap.SugaredLogger

	AuthFile   string
	AuthCookie string

	Host string

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

func (c *Huawei) Debug() {

}

func (c *Huawei) Start() {
	c.Log.Debug("??????????????????????????????")

	// ??????????????????
	if isExist(c.AuthFile) {
		c.Log.Debug("???????????????????????????")
		if cookie, err := ioutil.ReadFile(c.AuthFile); err != nil {
			c.Log.Errorf("??????????????????????????????, ??????????????????, error: %v", err)
			if err := c.Login(); err != nil {
				c.Log.Errorf("????????????, ?????????, error: %v", err)
				return
			}
		} else {
			// ??????????????????????????????
			if len(cookie) > 0 {
				c.AuthCookie = string(cookie)
			} else {
				c.Log.Debug("????????????????????????, ??????????????????")
				if err := c.Login(); err != nil {
					c.Log.Errorf("????????????, ?????????, error: %v", err)
					return
				}
			}
		}
	} else {
		c.Log.Debug("??????????????????????????????, ??????????????????")
		if err := c.Login(); err != nil {
			c.Log.Errorf("????????????, ?????????, error: %v", err)
			return
		}
	}

	// ???????????????
	c.CrawlerData.SectorSize = 512

	// ????????????
	if err := c.GetServerStatus(); err != nil {
		return
	}
	// ??????????????????
	if err := c.GetSystemInfo(); err != nil {
		return
	}
	// ???????????????
	if err := c.GetStoragePoolInfo(); err != nil {
		return
	}
	// ????????????
	if err := c.GetFanInfo(); err != nil {
		return
	}
	// ????????????
	if err := c.GetPowerInfo(); err != nil {
		return
	}
	// FC????????????
	if err := c.GetFcPortInfo(); err != nil {
		return
	}

	c.CrawlerData.PrintFile("huawei_text.txt")

	// ?????????????????????????????????????????????
	if err := c.GetCurrentState(); err != nil {
		return
	}
}

func (c *Huawei) Login() error {
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	// ??????????????????
	params := map[string]interface{}{
		"scope":     0,
		"username":  c.Username,
		"password":  c.Password,
		"isEncrypt": true,
		"loginMode": 3,
	}
	paramsJson, err := json.Marshal(params)
	if err != nil {
		c.Log.Errorf("JSON???????????????, %v, error: %v", params, err)
		return err
	}

	// ??????????????????
	loginUrl := c.Host + "/deviceManager/rest/xxxxx/login"
	request, _ := http.NewRequest("POST", loginUrl, bytes.NewReader(paramsJson))
	request.Header.Set("Content-Type", "application/json;charset=UTF-8")
	request.Header.Set("Cookie", c.AuthCookie)

	// ????????????
	if resp, err := client.Do(request); err != nil {
		c.Log.Errorf("????????????, params: %v, error: %v", params, err)
		return err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()

		cookies := resp.Cookies()
		if len(cookies) > 0 {
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				c.Log.Errorf("??????????????????????????????, params: %v, error: %v", params, err)
				return err
			}

			c.DeviceId = gjson.Get(string(body), "data.deviceid").String()
			c.AuthCookie = cookies[0].Name + "=" + cookies[0].Value
			if err := ioutil.WriteFile(c.AuthFile, []byte(c.AuthCookie), os.ModePerm); err != nil {
				c.Log.Errorf("?????????????????????????????????, error: %v", err)
				return err
			}
			return nil
		} else {
			return errors.New("???????????????????????????cookie")
		}
	}
}

func (c *Huawei) RequestJson(method, url string, params io.Reader) (string, error) {
	// ?????????????????????
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	request, err := http.NewRequest(method, url, params)
	if err != nil {
		c.Log.Errorf("??????????????????, error: %v", err)
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", c.AuthCookie)

	if resp, err := client.Do(request); err != nil {
		c.Log.Errorf("??????????????????, url: %v, error: %v", url, err)
		return "", err
	} else {
		defer func() {
			_ = resp.Body.Close()
		}()

		// ??????HTTP?????????
		if code := resp.StatusCode; code != 200 {
			c.Log.Errorf("??????????????????, url, %s, ?????????: %d, ????????????: %v", url, code, resp.Header)
			return "", errors.New(fmt.Sprintf("??????????????????, url, %s, ?????????: %d, ????????????: %v", url, code, resp.Header))
		}
		if body, err := ioutil.ReadAll(resp.Body); err != nil {
			c.Log.Errorf("???????????????????????????, url: %s, error: %v", url, err)
			return "", err
		} else {
			// ?????????????????????
			errorCode := gjson.Get(string(body), "error.code").String()
			if errorCode != "0" {
				if errorCode == "-401" {
					_ = os.Remove(c.AuthFile)
					c.Log.Errorf("??????????????????, ??????cookie??????, ???????????????")
					return "", errors.New("??????????????????")
				} else {
					c.Log.Errorf("????????????, url: %s, ?????????: %s, ????????????: %s",
						url, errorCode, gjson.Get(string(body), "error.description").String())
					return "", errors.New("????????????")
				}
			} else {
				return string(body), nil
			}
		}
	}
}

func (c *Huawei) GetServerStatus() error {
	c.Log.Debug("[REST]????????????")

	requestUrl := fmt.Sprintf("%s/deviceManager/rest/%s/server/status?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]????????????????????????, error: %v", err)
		return err
	} else {
		// ????????????
		status := gjson.Get(data, "data.status").String()
		c.CrawlerData.ServerStatus = status

		// ??????????????????
		description := gjson.Get(data, "data.description").String()
		c.CrawlerData.ServerStatusDescription = description

		return nil
	}
}

func (c *Huawei) GetSystemInfo() error {
	c.Log.Debug("[REST]????????????")

	requestUrl := fmt.Sprintf("%s/deviceManager/rest/%s/system/?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]????????????????????????, error: %v", err)
		return err
	} else {
		// ????????????
		productMode := gjson.Get(data, "data.PRODUCTMODE").String()
		productModeEnum := gjson.Get(HuaweiEnumDefine, "PRODUCT_MODE_E").Map()
		for k, v := range productModeEnum {
			if v.String() == productMode {
				c.CrawlerData.ProductMode = k
			}
		}

		// ???????????????
		c.GetDiskPoolForSystem()
		usableDiskpoolCapacityData := c.CrawlerData.UsableDiskpoolCapacityData
		// ????????????
		sectorSize := gjson.Get(data, "data.SECTORSIZE").Int()
		c.CrawlerData.SectorSize = sectorSize
		usedRawCapacity := gjson.Get(data, "data.MEMBERDISKSCAPACITY").Int()
		unusedRawCapacity := gjson.Get(data, "data.FREEDISKSCAPACITY").Int()
		usedCapacity := usedRawCapacity - usableDiskpoolCapacityData
		unusedCapacity := unusedRawCapacity + usableDiskpoolCapacityData

		// ???????????????Byte???
		c.CrawlerData.SystemCapacity = (usedCapacity + unusedCapacity) * sectorSize

		// ????????????????????????Byte???
		c.CrawlerData.SystemUsedCapacity = usedCapacity * sectorSize

		// ???????????????
		c.GetStoragePoolForSystem()
		// LUN
		lunCapacity := c.CrawlerData.LunCapacity
		c.CrawlerData.LunCapacity = lunCapacity * sectorSize

		// ????????????
		filesystemCapacity := c.CrawlerData.FilesystemCapacity
		c.CrawlerData.FilesystemCapacity = filesystemCapacity * sectorSize

		// ????????????
		dataProtectCapacity := c.CrawlerData.DataProtectCapacity
		c.CrawlerData.DataProtectCapacity = dataProtectCapacity * sectorSize

		// ????????????
		freePoolCapacity := c.CrawlerData.FreePoolCapacity
		c.CrawlerData.FreePoolCapacity = freePoolCapacity * sectorSize

		// ???????????????
		usableCapacity := lunCapacity + filesystemCapacity + dataProtectCapacity + freePoolCapacity
		c.CrawlerData.UsableCapacity = usableCapacity * sectorSize

		// ???????????????
		// c.CrawlerData.TotalCapacity

		return nil
	}
}

func (c *Huawei) GetDiskPoolForSystem() {
	c.Log.Debug("[REST]???????????????")

	requestUrl := fmt.Sprintf("%s/deviceManager/rest/%s/diskpool?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]???????????????????????????, error: %v", err)
	} else {
		// ????????????
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			// ????????????
			c.CrawlerData.UsableDiskpoolCapacityData = c.CrawlerData.UsableDiskpoolCapacityData + gjson.Get(string(value), "FREECAPACITY").Int()
		}, "data")
	}
}

func (c *Huawei) GetStoragePoolForSystem() {
	c.Log.Debug("[REST]???????????????(For ????????????)")

	requestUrl := fmt.Sprintf("%s/deviceManager/rest/%s/storagepool?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]?????????????????????(For ????????????)??????, error: %v", err)
	} else {
		// ????????????
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			// ??????????????????
			userConsumedCapacity := gjson.Get(string(value), "USERCONSUMEDCAPACITY").Int()
			usageType := gjson.Get(string(value), "USAGETYPE").String()
			if usageType == "1" {
				// LUN
				c.CrawlerData.LunCapacity = c.CrawlerData.LunCapacity + userConsumedCapacity
				// ???????????????
				c.CrawlerData.TotalCapacity = c.CrawlerData.TotalCapacity + gjson.Get(string(value), "LUNCONFIGEDCAPACITY").Int()
			} else if usageType == "2" {
				// ????????????
				c.CrawlerData.FilesystemCapacity = c.CrawlerData.FilesystemCapacity + userConsumedCapacity
				// ???????????????
				c.CrawlerData.TotalCapacity = c.CrawlerData.TotalCapacity + gjson.Get(string(value), "TOTALFSCAPACITY").Int()
			}

			// ????????????
			c.CrawlerData.DataProtectCapacity = c.CrawlerData.DataProtectCapacity + gjson.Get(string(value), "REPLICATIONCAPACITY").Int()

			// ????????????
			c.CrawlerData.FreePoolCapacity = c.CrawlerData.FreePoolCapacity + gjson.Get(string(value), "USERFREECAPACITY").Int()
		}, "data")
	}
}

func (c *Huawei) GetStoragePoolInfo() error {
	c.Log.Debug("[REST]???????????????")

	requestUrl := fmt.Sprintf("%s/deviceManager/rest/%s/storagepool?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]???????????????????????????, error: %v", err)
		return err
	} else {
		// ????????????
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			// ???????????????
			storagePoolInfo := make(map[string]interface{})

			storagePoolInfo["id"] = gjson.Get(string(value), "ID").String()
			storagePoolInfo["name"] = gjson.Get(string(value), "NAME").String()

			// ????????????
			healthStatus := gjson.Get(string(value), "HEALTHSTATUS").String()
			healthStatusEnum := gjson.Get(HuaweiEnumDefine, "HEALTH_STATUS_E").Map()
			for k, v := range healthStatusEnum {
				if v.String() == healthStatus {
					storagePoolInfo["healthStatus"] = k
				}
			}

			// ????????????
			runningStatus := gjson.Get(string(value), "RUNNINGSTATUS").String()
			runningStatusEnum := gjson.Get(HuaweiEnumDefine, "RUNNING_STATUS_E").Map()
			for k, v := range runningStatusEnum {
				if v.String() == runningStatus {
					storagePoolInfo["runningStatus"] = k
				}
			}

			// ????????????
			userFreeCapacity := gjson.Get(string(value), "USERFREECAPACITY").Int()
			storagePoolInfo["userFreeCapacity"] = userFreeCapacity * c.CrawlerData.SectorSize

			// ?????????
			userTotalCapacity := gjson.Get(string(value), "USERTOTALCAPACITY").Int()
			storagePoolInfo["userTotalCapacity"] = userTotalCapacity * c.CrawlerData.SectorSize

			c.CrawlerData.StoragePoolInfo = append(c.CrawlerData.StoragePoolInfo, storagePoolInfo)
		}, "data")

		return nil
	}
}

func (c *Huawei) GetFanInfo() error {
	c.Log.Debug("[REST]????????????")

	requestUrl := fmt.Sprintf("%s/deviceManager/rest/%s/fan?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]????????????????????????, error: %v", err)
		return err
	} else {
		// ????????????
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			fanInfo := make(map[string]interface{})

			fanInfo["id"] = gjson.Get(string(value), "ID").String()
			fanInfo["name"] = gjson.Get(string(value), "NAME").String()

			fanInfo["location"] = gjson.Get(string(value), "LOCATION").String()

			// ????????????
			healthStatus := gjson.Get(string(value), "HEALTHSTATUS").String()
			healthStatusEnum := gjson.Get(HuaweiEnumDefine, "HEALTH_STATUS_E").Map()
			for k, v := range healthStatusEnum {
				if v.String() == healthStatus {
					fanInfo["healthStatus"] = k
				}
			}

			// ????????????
			runningStatus := gjson.Get(string(value), "RUNNINGSTATUS").String()
			runningStatusEnum := gjson.Get(HuaweiEnumDefine, "RUNNING_STATUS_E").Map()
			for k, v := range runningStatusEnum {
				if v.String() == runningStatus {
					fanInfo["runningStatus"] = k
				}
			}

			c.CrawlerData.FanInfo = append(c.CrawlerData.FanInfo, fanInfo)
		}, "data")

		return nil
	}
}

func (c *Huawei) GetPowerInfo() error {
	c.Log.Debug("[REST]????????????")

	requestUrl := fmt.Sprintf("%s/deviceManager/rest/%s/power?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]????????????????????????, error: %v", err)
		return err
	} else {
		// ????????????
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			powerInfo := make(map[string]interface{})

			powerInfo["id"] = gjson.Get(string(value), "ID").String()
			powerInfo["name"] = gjson.Get(string(value), "NAME").String()

			// ????????????
			healthStatus := gjson.Get(string(value), "HEALTHSTATUS").String()
			healthStatusEnum := gjson.Get(HuaweiEnumDefine, "HEALTH_STATUS_E").Map()
			for k, v := range healthStatusEnum {
				if v.String() == healthStatus {
					powerInfo["healthStatus"] = k
				}
			}

			// ????????????
			runningStatus := gjson.Get(string(value), "RUNNINGSTATUS").String()
			runningStatusEnum := gjson.Get(HuaweiEnumDefine, "RUNNING_STATUS_E").Map()
			for k, v := range runningStatusEnum {
				if v.String() == runningStatus {
					powerInfo["runningStatus"] = k
				}
			}

			c.CrawlerData.PowerInfo = append(c.CrawlerData.PowerInfo, powerInfo)
		}, "data")

		return nil
	}
}

func (c *Huawei) GetFcPortInfo() error {
	c.Log.Debug("[REST]FC????????????")

	requestUrl := fmt.Sprintf("%s/deviceManager/rest/%s/fc_port?t=%d", c.Host, c.DeviceId, time.Now().UnixNano()/1e6)
	if data, err := c.RequestJson("GET", requestUrl, nil); err != nil {
		c.Log.Errorf("[REST]??????FC??????????????????, error: %v", err)
		return err
	} else {
		// ????????????
		_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
			fcPortInfo := make(map[string]interface{})

			fcPortInfo["id"] = gjson.Get(string(value), "ID").String()
			fcPortInfo["name"] = gjson.Get(string(value), "NAME").String()

			// ????????????
			healthStatus := gjson.Get(string(value), "HEALTHSTATUS").String()
			healthStatusEnum := gjson.Get(HuaweiEnumDefine, "HEALTH_STATUS_E").Map()
			for k, v := range healthStatusEnum {
				if v.String() == healthStatus {
					fcPortInfo["healthStatus"] = k
				}
			}

			// ????????????
			runningStatus := gjson.Get(string(value), "RUNNINGSTATUS").String()
			runningStatusEnum := gjson.Get(HuaweiEnumDefine, "RUNNING_STATUS_E").Map()
			for k, v := range runningStatusEnum {
				if v.String() == runningStatus {
					fcPortInfo["runningStatus"] = k
				}
			}

			c.CrawlerData.FcPortInfo = append(c.CrawlerData.FcPortInfo, fcPortInfo)
		}, "data")

		return nil
	}
}

func (c *Huawei) GetCurrentState() error {
	// ????????????
	indexList := []string{"fc_port", "disk", "diskpool", "lun"}

	// ???IOPS,???IOPS,???IOPS,??????IOPS,?????????,?????????
	baseDataIdList := "22,25,28,307,23,26"
	baseUrl := fmt.Sprintf("%s/deviceManager/rest/%s/performace_statistic/cur_statistic_data", c.Host, c.DeviceId)
	for i := 0; i < len(indexList); i++ {
		index := indexList[i]

		// ????????????
		uuidList := make([]string, 0)
		listUrl := fmt.Sprintf("%s/deviceManager/rest/%s/%s?t=%d", c.Host, c.DeviceId, index, time.Now().UnixNano()/1e6)
		if data, err := c.RequestJson("GET", listUrl, nil); err != nil {
			c.Log.Errorf("[REST]??????[%s]??????????????????, error: %v", index, err)
			return err
		} else {
			_, _ = jsonparser.ArrayEach([]byte(data), func(value []byte, valueType jsonparser.ValueType, offset int, err error) {
				dataType := gjson.Get(string(value), "TYPE").String()
				dataId := gjson.Get(string(value), "ID").String()

				uuidList = append(uuidList, dataType+":"+dataId)
			}, "data")
		}

		// ????????????
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
				c.Log.Errorf("[REST]??????[%s]??????????????????, error: %v", index, err)
				return err
			} else {
				c.Log.Debugf("[REST]UUID[%s]???[%s]??????????????????, %s", index, uuid, gjson.Get(data, "data.0"))
			}
		}
	}

	return nil
}
