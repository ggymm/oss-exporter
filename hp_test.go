package main

import (
	"crypto/md5"
	_ "embed"
	"encoding/hex"
	"fmt"
	"github.com/beevik/etree"
	"strconv"
	"testing"

	"github.com/robertkrimen/otto"
)

func TestHP_MD5(t *testing.T) {
	h := md5.New()
	h.Write([]byte("manage_!manage"))
	fmt.Println("539e12f63b693a9970a97b885e857f8b" == hex.EncodeToString(h.Sum(nil)))
}

var (
	//go:embed test_resp.xml
	TestResp string
)

func TestHP_ParseResp(t *testing.T) {
	// 执行JS函数处理返回值
	// 格式化成JSON
	vm := otto.New()
	_, err := vm.Run(HPScript)
	if err != nil {
		t.Errorf("解析请求体数据失败[加载脚本], error: %v", err)
	}
	if respJsObj, err := vm.Object(TestResp); err != nil {
		t.Errorf("解析请求体数据失败[响应字符串转JS对象], error: %v", err)
	} else {
		value, err := vm.Call("ObjectToJson", nil, respJsObj)
		if err != nil {
			t.Errorf("解析请求体数据失败[执行解析方法], error: %v", err)
		}
		t.Log(value.String())
	}
}

func TestHP_GetComponentState(t *testing.T) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(TestResp); err != nil {
		t.Errorf("[REST]解析组件状态响应数据失败, error: %v", err)
		return
	}

	controllerElems := doc.FindElements("/RESPONSE/OBJECT/OBJECT[@basetype='controllers']")
	for i := 0; i < len(controllerElems); i++ {
		id := controllerElems[i].FindElement("./PROPERTY[@name='controller-id']").Text()
		health := controllerElems[i].FindElement("./PROPERTY[@name='health']").Text()

		t.Log("控制器状态", id, health)

		childElems := []string{
			"./OBJECT[@basetype='network-parameters']", // 网络端口状态
			"./OBJECT[@basetype='port']",               // 主机端口状态
			"./OBJECT[@basetype='expander-ports']",     // 扩展端口状态
			"./OBJECT[@basetype='compact-flash']",      // CompactFlash状态
		}
		for j := 0; j < len(childElems); j++ {
			stateElems := controllerElems[i].FindElements(childElems[j])
			for k := 0; k < len(stateElems); k++ {
				id := stateElems[k].FindElement("./PROPERTY[@name='durable-id']").Text()
				health := stateElems[k].FindElement("./PROPERTY[@name='health']").Text()

				switch j {
				case 0:
					t.Log("网络端口状态", id, health)
				case 1:
					t.Log("主机端口状态", id, health)
				case 2:
					t.Log("扩展端口状态", id, health)
				case 3:
					t.Log("CompactFlash状态", id, health)
				}
			}
		}
	}

	powerSuppliesElems := doc.FindElements("/RESPONSE/OBJECT/OBJECT[@basetype='power-supplies']")
	for i := 0; i < len(powerSuppliesElems); i++ {
		id := powerSuppliesElems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
		health := powerSuppliesElems[i].FindElement("./PROPERTY[@name='health']").Text()

		t.Log("电源状态", id, health)

		fElems := powerSuppliesElems[i].FindElements("./OBJECT[@basetype='fan']")
		for i := 0; i < len(fElems); i++ {
			id := fElems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
			health := fElems[i].FindElement("./PROPERTY[@name='health']").Text()

			t.Log("风扇状态", id, health)
		}
	}
}

func TestHP_GetDiskInfo(t *testing.T) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(TestResp); err != nil {
		t.Errorf("[REST]解析磁盘状态响应数据失败, error: %v", err)
		return
	}

	var (
		sizeTotal   int
		sizeSpares  int
		sizeVirtual int
	)

	elems := doc.FindElements("/RESPONSE/OBJECT[@basetype='drives']")
	for i := 0; i < len(elems); i++ {
		// 使用情况
		usageNumeric := elems[i].FindElement("./PROPERTY[@name='usage-numeric']").Text()
		// 大小
		sizeNumeric := elems[i].FindElement("./PROPERTY[@name='size-numeric']").Text()
		sizeNumericInt, _ := strconv.Atoi(sizeNumeric)

		sizeTotal += sizeNumericInt * 512
		switch usageNumeric {
		case "2", "3":
			sizeSpares += sizeNumericInt * 512
		case "9":
			sizeVirtual += sizeNumericInt * 512
		}
	}

	t.Log(sizeTotal, sizeSpares, sizeVirtual)
}

func TestHP_GetPoolInfo(t *testing.T) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(TestResp); err != nil {
		t.Errorf("[REST]请求存储池信息失败, error: %v", err)
		return
	}

	var virtPoolAllocSizeTotal int64

	elems := doc.FindElements("/RESPONSE/OBJECT[@basetype='pools']")
	for i := 0; i < len(elems); i++ {
		// 页面大小（块）
		pageSize := elems[i].FindElement("./PROPERTY[@name='page-size-numeric']").Text()
		pageSizeInt, _ := strconv.Atoi(pageSize)
		// 分配的页数
		allocatedPages := elems[i].FindElement("./PROPERTY[@name='allocated-pages']").Text()
		allocatedPagesInt, _ := strconv.ParseInt(allocatedPages, 10, 64)

		virtPoolAllocSizeTotal += int64(pageSizeInt) * allocatedPagesInt * 512
	}

	t.Log(virtPoolAllocSizeTotal)
}

func TestHP_GetVolumeGroupInfo(t *testing.T) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(TestResp); err != nil {
		t.Errorf("[REST]请求卷组信息失败, error: %v", err)
		return
	}

	var (
		totalSize            int64
		virtUnallocSizeTotal int64
	)

	elems := doc.FindElements("/RESPONSE/OBJECT/OBJECT[@basetype='volumes']")
	for i := 0; i < len(elems); i++ {
		volumeTypeNumeric := elems[i].FindElement("./PROPERTY[@name='volume-type-numeric']").Text()

		sizeNumeric := elems[i].FindElement("./PROPERTY[@name='size-numeric']").Text()
		sizeNumericInt, _ := strconv.ParseInt(sizeNumeric, 10, 64)

		if volumeTypeNumeric == "0" ||
			volumeTypeNumeric == "2" ||
			volumeTypeNumeric == "4" ||
			volumeTypeNumeric == "8" ||
			volumeTypeNumeric == "13" ||
			volumeTypeNumeric == "15" {
			totalSize += sizeNumericInt
		}
	}

	// 11832412602368
	t.Log(totalSize, virtUnallocSizeTotal)
}
