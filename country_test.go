package main

import (
	"fmt"
	"testing"
)

func TestNew(t *testing.T) {
	LoadGeoDB("./GeoLite2-Country.mmdb")

	fmt.Println(GetCountryIsoCode("39.106.101.133"))
	fmt.Println(GetCountryIsoCode(""))
	fmt.Println(GetCountryIsoCode("aa"))
	fmt.Println(GetCountryIsoCode("39.106.101.133:14234"))
	fmt.Println(GetCountryIsoCode("192.168.1.121"))
}
