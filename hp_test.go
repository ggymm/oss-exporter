package main

import (
	"crypto/md5"
	_ "embed"
	"encoding/hex"
	"fmt"
	"github.com/beevik/etree"
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
	}

	controllerElems := doc.FindElements("/RESPONSE/OBJECT/OBJECT[@basetype='controllers']")
	for i := 0; i < len(controllerElems); i++ {
		id := controllerElems[i].FindElement("./PROPERTY[@name='controller-id']").Text()
		health := controllerElems[i].FindElement("./PROPERTY[@name='health']").Text()

		t.Log("控制器状态", id, health)

		npElems := controllerElems[i].FindElements("./OBJECT[@basetype='network-parameters']")
		for i := 0; i < len(npElems); i++ {
			id := npElems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
			health := npElems[i].FindElement("./PROPERTY[@name='health']").Text()

			t.Log("网络端口状态", id, health)
		}

		pElems := controllerElems[i].FindElements("./OBJECT[@basetype='port']")
		for i := 0; i < len(pElems); i++ {
			id := pElems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
			health := pElems[i].FindElement("./PROPERTY[@name='health']").Text()

			t.Log("主机端口状态", id, health)
		}

		epElems := controllerElems[i].FindElements("./OBJECT[@basetype='expander-ports']")
		for i := 0; i < len(epElems); i++ {
			id := epElems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
			health := epElems[i].FindElement("./PROPERTY[@name='health']").Text()

			t.Log("扩展端口状态", id, health)
		}

		cfElems := controllerElems[i].FindElements("./OBJECT[@basetype='compact-flash']")
		for i := 0; i < len(cfElems); i++ {
			id := cfElems[i].FindElement("./PROPERTY[@name='durable-id']").Text()
			health := cfElems[i].FindElement("./PROPERTY[@name='health']").Text()

			t.Log("CompactFlash状态", id, health)
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
