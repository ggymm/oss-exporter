package main

import (
	"fmt"
	"testing"
)

func TestString(t *testing.T) {
	str := "JSESSIONID=524DD2D0127E4A087CE55958AF5560A9;_sync=nfvp17re5ash8e2vm9i4f18mfs;"
	fmt.Println(str[:1])
}
