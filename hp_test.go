package main

import (
	"crypto/md5"
	_ "embed"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/robertkrimen/otto"
)

func TestHP_MD5(t *testing.T) {
	h := md5.New()
	h.Write([]byte("manage_!manage"))
	fmt.Println("539e12f63b693a9970a97b885e857f8b" == hex.EncodeToString(h.Sum(nil)))
}

var (
	//go:embed test_resp.txt
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
