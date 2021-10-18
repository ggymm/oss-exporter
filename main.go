package main

import (
	"flag"
	"fmt"
	"os"
)

func init() {
	if !isExist("cookie") {
		_ = os.Mkdir("cookie", os.ModePerm)
	}
}

func main() {
	var ossType string
	flag.StringVar(&ossType, "oss-type", "", "")
	flag.Parse()

	if len(ossType) == 0 {
		fmt.Printf("启动参数错误, 示例: oss-exporter --oss-type=ibm(huawei,hp,dell)")
		return
	}

	switch ossType {
	case "ibm":
		// IBM存储设备数据抓取
		if crawler, err := NewIbmV7000Crawler(); err != nil {
			fmt.Printf("初始化IbmV7000任务失败, %v", err)
			return
		} else {
			crawler.Start()
		}
	case "huawei":
		// 华为存储设备数据抓取
		if crawler, err := NewHuaweiCrawler(); err != nil {
			fmt.Printf("初始化华为任务失败, %v", err)
			return
		} else {
			crawler.Start()
		}
	case "hp":
		// 惠普存储设备数据抓取
		if crawler, err := NewHPCrawler(); err != nil {
			fmt.Printf("初始化惠普任务失败, %v", err)
			return
		} else {
			crawler.Start()
		}
	case "dell":
		// 戴尔存储设备数据抓取
		if crawler, err := NewDellCrawler(); err != nil {
			fmt.Printf("初始化戴尔任务失败, %v", err)
			return
		} else {
			crawler.Start()
		}
	}
}
