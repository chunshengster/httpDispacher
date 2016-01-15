package main

import (
	//"log"
	"os"
	"runtime"
	"runtime/pprof"
	"server"
	"utils"
)

// param: lower case + Upper Case ,No _ spliter
// Struct unit: Upper Case
// Func: golang style

func main() {
	f, err := os.Create("./cpuprofile.out")
	if err != nil {
		//log.Fatal(err)
                utils.ServerLogger.Critical("Create cpuprofile fail: %s", err.Error())
                os.Exit(1)
	}

	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()
	runtime.GOMAXPROCS(8)
	utils.InitUitls()
	//	utils.Logger.Println(ServerAddr + ":" + ServerPort)
	server.NewServer()

}
