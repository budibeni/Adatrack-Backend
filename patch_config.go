package main

import (
	"fmt"
	"io/ioutil"
	"strings"
)

func main() {
	b, _ := ioutil.ReadFile("internal/config/config.go")
	s := string(b)
	
	newFields := `
	PortGT06      string
	PortTeltonika string
	PortCoban     string
	PortMeitrack  string
	PortH02       string
	PortMeiligao  string
	PortXexun     string
	PortSuntech   string
	PortTotem     string
	PortGT02      string
	PortNavigil   string
	PortCastel    string
`
	s = strings.Replace(s, `
	PortGT06      string
	PortTeltonika string
	PortCoban     string
	PortMeitrack  string
	PortH02       string`, newFields, 1)

	newInit := `
		PortGT06:      getEnv("GT06_TCP_PORT", getEnv("PORT_GT06", "5023")),
		PortTeltonika: getEnv("TELTONIKA_TCP_PORT", getEnv("PORT_TELTONIKA", "5027")),
		PortCoban:     getEnv("TK103_TCP_PORT", getEnv("PORT_COBAN", "5013")),
		PortMeitrack:  getEnv("MEITRACK_TCP_PORT", getEnv("PORT_MEITRACK", "5020")),
		PortH02:       getEnv("H02_TCP_PORT", getEnv("PORT_H02", "5010")),
		PortMeiligao:  getEnv("MEILIGAO_TCP_PORT", "5002"),
		PortXexun:     getEnv("XEXUN_TCP_PORT", "5003"),
		PortSuntech:   getEnv("SUNTECH_TCP_PORT", "5017"),
		PortTotem:     getEnv("TOTEM_TCP_PORT", "5005"),
		PortGT02:      getEnv("GT02_TCP_PORT", "5006"),
		PortNavigil:   getEnv("NAVIGIL_TCP_PORT", "5012"),
		PortCastel:    getEnv("CASTEL_TCP_PORT", "5019"),
`
	s = strings.Replace(s, `
		PortGT06:      getEnv("PORT_GT06", "15000"),
		PortTeltonika: getEnv("PORT_TELTONIKA", "15001"),
		PortCoban:     getEnv("PORT_COBAN", "15002"),
		PortMeitrack:  getEnv("PORT_MEITRACK", "15003"),
		PortH02:       getEnv("PORT_H02", "15004"),`, newInit, 1)

	ioutil.WriteFile("internal/config/config.go", []byte(s), 0644)
	fmt.Println("Done")
}
