package main

import "fmt"

func main() {
	if ibmV7000Crawler, err := NewIbmV7000Crawler(); err != nil {
		fmt.Printf("初始化IbmV7000任务失败, %v", err)
		return
	} else {
		fmt.Println(ibmV7000Crawler)
		// ibmV7000Crawler.Start()
	}

	if huaweiCrawler, err := NewHuaweiCrawler(); err != nil {
		fmt.Printf("初始化Huawei任务失败, %v", err)
		return
	} else {
		huaweiCrawler.Start()
	}
}
