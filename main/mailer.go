could not import "/home/daemonl/go/src/github.com/daemonl/go_mailer/mailer": found packages mailer (Config.go) and main (Mailer.go) in /home/daemonl/go/src/github.com/daemonl/go_mailer/mailerpackage main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
)

var configFilename string

func init() {
	flag.StringVar(&configFilename, "config", "config.json", "The filename to load json config from")
}

func main() {
	flag.Parse()
	config := &mailer.Config{}
	err := readConfig(config)
	if err != nil{
		fmt.Println(err.Error())
		os.Exit(1)
		return
	}
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
