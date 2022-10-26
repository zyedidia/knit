package main

import (
	"fmt"
	"log"
	"os"

	lua "github.com/zyedidia/gopher-lua"
	"github.com/zyedidia/knit"
)

func main() {
	vm := knit.NewLuaVM()
	ret, err := vm.DoFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	switch ret := ret.(type) {
	case *lua.LUserData:
		switch v := ret.Value.(type) {
		case knit.LRuleSet:
			fmt.Println(v)
		case knit.LBuildSet:
			fmt.Println(v)
		default:
			log.Fatal("bad type")
		}
	default:
		log.Fatal("bad type")
	}
}
