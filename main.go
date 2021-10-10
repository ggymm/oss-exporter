package main

import "fmt"

func main() {
	// IBM存储设备数据抓取
	if crawler, err := NewIbmV7000Crawler(); err != nil {
		fmt.Printf("初始化IbmV7000任务失败, %v", err)
		return
	} else {
		crawler.Debug()
		// crawler.Start()
	}

	// 华为存储设备数据抓取
	if crawler, err := NewHuaweiCrawler(); err != nil {
		fmt.Printf("初始化Huawei任务失败, %v", err)
		return
	} else {
		crawler.Debug()
		// crawler.Start()
	}

	// 惠普存储设备数据抓取
	if crawler, err := NewHPCrawler(); err != nil {
		return
	} else {
		crawler.Start()
	}
}
