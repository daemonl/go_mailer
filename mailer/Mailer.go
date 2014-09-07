package main

import (
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"

	"github.com/daemonl/go_sweetpl"
	_ "github.com/go-sql-driver/mysql"
)

type EmailData struct {
	ID        uint64
	FirstName *string
	LastName  *string
	Email     *string
}

var configFilename string
var tpl *sweetpl.SweeTpl
var config *Config
var tlsConfig *tls.Config
var simpleAuth smtp.Auth
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

	simpleAuth = smtp.PlainAuth("", config.SMTP.Username, config.SMTP.Password, config.SMTP.Hello)

	tlsConfig = &tls.Config{
		ServerName: config.SMTP.Hello,
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

	err = doMailLoop()
	if err != nil {
		fmt.Printf("Error in loop: %s\n", err.Error())
		os.Exit(1)
		return
	}
}

func getSmtpClient() (*smtp.Client, error) {
	log.Printf("Dial %s\n", config.SMTP.Server)
	smtpClient, err := smtp.Dial(config.SMTP.Server)
	if err != nil {
		return nil, err
	}
	if err := smtpClient.Hello(config.SMTP.Hello); err != nil {
		return nil, err
	}
	log.Println("STARTTLS")
	if err := smtpClient.StartTLS(tlsConfig); err != nil {
		return nil, err
	}
	/*
		if err := smtpClient.Hello(config.SMTP.Hello); err != nil {
			return nil, err
		}*/

	if err := smtpClient.Auth(simpleAuth); err != nil {
		return nil, fmt.Errorf("AUTH: %s", err.Error())
	}
	log.Println("SMTP Client Connected")
	return smtpClient, nil
}

func doMailLoop() error {

	smtpClient, err := getSmtpClient()
	if err != nil {
		return fmt.Errorf("Creating SMTP Client: %s", err.Error())
	}

	res, err := db.Query(fmt.Sprintf(`
	SELECT id, first, last, email 
	FROM %s
	WHERE send1 IS NULL
	AND unsubscribe IS NULL
	AND fail IS NULL`, config.Table))

	if err != nil {
		return err
	}

	var numberSent int = 0
	for res.Next() {

		if numberSent > 10 {
			// Establish new smtp client.

			if err := smtpClient.Quit(); err != nil {
				return fmt.Errorf("Quit: %s", err.Error())
			}
			/*
				if err := smtpClient.Close(); err != nil {
					return fmt.Errorf("Close: %s", err.Error())
				}*/
			smtpClient, err = getSmtpClient()
			if err != nil {
				return fmt.Errorf("GET Client: %s", err.Error())
			}
			numberSent = 0
		}

		numberSent++

		data := &EmailData{}

		err := res.Scan(&data.ID, &data.FirstName, &data.LastName, &data.Email)
		if err != nil {
			log.Println(err.Error())
			continue
		}
		_, err = db.Exec(fmt.Sprintf(`UPDATE %s SET send1 = 1 WHERE id = ?`, config.Table), data.ID)
		if err != nil {
			log.Println(err.Error())
			continue
		}

		err = doMail(smtpClient, data)
		if err != nil {
			log.Println(err.Error())
			continue
		}

		_, err = db.Exec(fmt.Sprintf(`UPDATE %s SET send1 = 2 WHERE id = ?`, config.Table), data.ID)
		if err != nil {
			log.Println(err.Error())
			continue
		}
	}

	smtpClient.Quit()
	smtpClient.Close()
	return nil
}

func doMail(smtpClient *smtp.Client, data *EmailData) error {
	if err := smtpClient.Reset(); err != nil {
		return err
	}
	if data.Email == nil {
		return fmt.Errorf("NO Email address for %d", data.ID)
	}
	log.Printf("SEND TO %s\n", *data.Email)

	if err := smtpClient.Mail(config.SMTP.From); err != nil {
		return err
	}
	if err := smtpClient.Rcpt(*data.Email); err != nil {
		return err
	}
	writer, err := smtpClient.Data()
	if err != nil {
		return err
	}

	err = doEmail(writer, data) //writer, data)
	if err != nil {
		return err
	}
	err = writer.Close()
	if err != nil {
		return err
	}
	return nil
}

func doEmail(w io.Writer, data *EmailData) error {

	var err error

	mw := multipart.NewWriter(w)

	unsubscribe := config.ListUnsubscribe
	if strings.Contains(unsubscribe, "%s") {
		b64e := base64.URLEncoding.EncodeToString([]byte(*data.Email))
		unsubscribe = fmt.Sprintf(unsubscribe, b64e)
	}
	headers := map[string]string{
		"From":             config.From, //`"OSMAD" <info@osmad.com.au>`,
		"To":               *data.Email,
		"Subject":          config.Subject,
		"List-Unsubscribe": unsubscribe,
		"MIME-Version":     "1.0",
		"Content-Type":     `multipart/alternative; boundary="` + mw.Boundary() + `"`,
		"Precedence":       "bulk",
	}

	for key, val := range headers {
		fmt.Fprintf(w, "%s: %s\n", key, val)
	}

	fmt.Fprintln(w, "")

	hdr1 := textproto.MIMEHeader{}
	hdr1.Add("Content-Type", "text/plain")
	textPart, err := mw.CreatePart(hdr1)
	if err != nil {
		return err
	}

	err = tpl.Render(textPart, config.TextTemplate, data)
	if err != nil {
		return err
	}

	hdr2 := textproto.MIMEHeader{}
	hdr2.Add("Content-Type", "text/html")
	htmlPart, err := mw.CreatePart(hdr2)
	if err != nil {
		return err
	}

	err = tpl.Render(htmlPart, config.EmailTemplate, data)
	if err != nil {
		return err
	}
	mw.Close()
	return nil
}
