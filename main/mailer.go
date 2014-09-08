package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/daemonl/go_mailer/mailer"
	"github.com/daemonl/go_mailer/parser"
)

var configFilename string
var function string

func init() {
	flag.StringVar(&function, "func", "", "The function to run (send, parse)")
	flag.StringVar(&configFilename, "config", "config.json", "The filename to load json config from")
}

func main() {
	flag.Parse()

	if function == "send" {
		err := send()
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
			return
		}
		return
	}

	if function == "parse" {
		err := parse()
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
			return
		}
		return
	}

	fmt.Printf("No function %s\n", function)
	os.Exit(2)
	return
}

func parse() error {
	config := &parser.Config{}
	err := readConfig(config)
	if err != nil {
		return err
	}
	p, err := parser.GetParser(config)
	if err != nil {
		return err
	}

	err = p.ParseFailures()
	if err != nil {
		return err
	}
	err = p.ParseUnsubscribes()
	if err != nil {
		return err
	}
	return nil

}

func send() error {
	config := &mailer.Config{}
	err := readConfig(config)
	if err != nil {
		return err
	}

	m, err := mailer.GetMailer(config)
	if err != nil {
		return err
	}

	err = m.DoMailLoop()
	if err != nil {
		return err
	}
	return nil
}

func readConfig(config interface{}) error {
	var configReader io.Reader
	var err error

	if configFilename == "-" {
		configReader = os.Stdin
	} else {
		configReader, err = os.Open(configFilename)
		if err != nil {
			return err
		}
	}

	decoder := json.NewDecoder(configReader)
	err = decoder.Decode(config)
	if err != nil {
		return err
	}
	return nil
}
