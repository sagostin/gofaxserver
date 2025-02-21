// This file is part of the GOfax.IP project - https://github.com/gonicus/gofaxip
// Copyright (C) 2014 GONICUS GmbH, Germany - http://www.gonicus.de
//
// This program is free software; you can redistribute it and/or
// modify it under the terms of the GNU General Public License
// as published by the Free Software Foundation; version 2
// of the License.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program; if not, write to the Free Software
// Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.

package main

import (
	"flag"
	"fmt"
	"github.com/gonicus/gofaxip/gofaxlib"
	"github.com/gonicus/gofaxip/gofaxlib/logger"
	"github.com/gonicus/gofaxip/gofaxserver"
	"log"
	"os"
)

const (
	defaultConfigfile = "/etc/gofaxserver/config.json"
	productName       = "gofaxserver"
)

var (
	configFile = flag.String("c", defaultConfigfile, "gofaxserver configuration file")
	// deviceID    = flag.String("m", "", "Virtual modem device ID")
	showVersion = flag.Bool("version", false, "Show version information")

	usage = fmt.Sprintf("Usage: %s -version | [-c configfile] -m deviceID qfile [qfile [qfile [...]]]", os.Args[0])

	// Version can be set at build time using:
	//    -ldflags "-X main.version 0.42"
	version string
)

func init() {
	if version == "" {
		version = "development version"
	}
	version = fmt.Sprintf("%v %v", productName, version)

	flag.Usage = func() {
		log.Printf("%s\n%s\n", version, usage)
		flag.PrintDefaults()
	}
}

func logPanic() {
	if r := recover(); r != nil {
		logger.Logger.Print(r)
		panic(r)
	}
}

func main() {
	defer logPanic()
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(1)
	}

	logger.Logger.Printf("%v gofaxserver %v starting", productName, version)
	gofaxlib.LoadConfig(*configFile)

	// Start event socket server to handle incoming calls
	server := gofaxserver.NewServer()
	go server.Start()

	select {}
}
