package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"os/exec"

	"github.com/daemonl/go_sweetpl"
	_ "github.com/go-sql-driver/mysql"
)

type Config struct {
	TemplatePath  string `json:"templateRoot"`
	EmailTemplate string `json:"emailTemplate"`
	TextTemplate  string `json:"textTemplate"`
	MailServer    string `json:"mailServer"`
	DSN           string `json:"dsn"`
	Table         string `json:"recipientTable"`
	Subject       string `json:"subject"`
}

type EmailData struct {
	ID        uint64
	FirstName *string
	LastName  *string
	Email     *string
}

var configFilename string
var tpl *sweetpl.SweeTpl
var config *Config
var db *sql.DB

func init() {
	flag.StringVar(&configFilename, "config", "config.json", "The filename to load json config from")
}

func setup() (*Config, error) {

	flag.Parse()

	var configReader io.Reader
	var err error

	config := &Config{}

	if configFilename == "-" {
		configReader = os.Stdin
	} else {
		configReader, err = os.Open(configFilename)
		if err != nil {
			return nil, err
		}
	}

	decoder := json.NewDecoder(configReader)
	err = decoder.Decode(config)
	if err != nil {
		return nil, err
	}

	db, err = sql.Open("mysql", config.DSN)
	if err != nil {
		return nil, err
	}

	return config, nil

}

func main() {
	var err error
	config, err = setup()
	if err != nil {
		fmt.Printf("Error authenticating: %s\n", err.Error())
		os.Exit(1)
		return
	}

	tpl = &sweetpl.SweeTpl{
		Loader: &sweetpl.DirLoader{
			BasePath: config.TemplatePath,
		},
	}

	res, err := db.Query(fmt.Sprintf(`
	SELECT id, first, last, email 
	FROM %s
	WHERE send1 IS NULL
	AND unsubscribe IS NULL
	AND fail IS NULL`, config.Table))

	if err != nil {
		fmt.Println(err.Error())
		return
	}

	for res.Next() {
		data := &EmailData{}
		err := res.Scan(&data.ID, &data.FirstName, &data.LastName, &data.Email)
		if err != nil {
			fmt.Println(err.Error())
			continue
		}
		_, err = db.Exec(fmt.Sprintf(`UPDATE %s SET send1 = 1 WHERE id = ?`, config.Table), data.ID)
		if err != nil {
			fmt.Println(err.Error())
			continue
		}

		err = doMail(data)
		if err != nil {
			fmt.Println(err.Error())
			continue
		}

		_, err = db.Exec(fmt.Sprintf(`UPDATE %s SET send1 = 2 WHERE id = ?`, config.Table), data.ID)
		if err != nil {
			fmt.Println(err.Error())
			continue
		}

	}

}

func doMail(data *EmailData) error {
	if data.Email == nil {
		return fmt.Errorf("NO Email address for %d", data.ID)
	}
	fmt.Printf("SEND TO %s\n", *data.Email)
	cmd := exec.Command("ssh", config.MailServer, "sendmail", *data.Email)
	w, _ := cmd.StdinPipe()
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Start()
	err := doEmail(w, data)
	if err != nil {
		return err
	}
	cmd.Wait()
	return nil
}

func doEmail(w io.Writer, data *EmailData) error {

	var err error

	mw := multipart.NewWriter(w)

	headers := map[string]string{
		"From":         `"OSMAD" <info@osmad.com.au>`,
		"To":           *data.Email,
		"Subject":      config.Subject,
		"MIME-Version": "1.0",
		"Content-Type": `multipart/alternative; 
        boundary="` + mw.Boundary() + `"`,
	}

	for key, val := range headers {
		fmt.Fprintf(w, "%s: %s\n", key, val)
	}

	hdr1 := textproto.MIMEHeader{}
	hdr1.Add("Content-Type", "text/plain")
	textPart, err := mw.CreatePart(hdr1)
	if err != nil {
		return err
	}

	err = tpl.Render(textPart, config.TextTemplate, data)
	if err != nil {
		fmt.Println(err.Error())
	}

	hdr2 := textproto.MIMEHeader{}
	hdr2.Add("Content-Type", "text/html")
	htmlPart, err := mw.CreatePart(hdr2)
	if err != nil {
		fmt.Println(err.Error())
	}

	err = tpl.Render(htmlPart, config.EmailTemplate, data)
	if err != nil {
		fmt.Println(err.Error())
	}
	mw.Close()
	fmt.Fprintln(w, ".")
	return nil
}
