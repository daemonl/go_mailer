package mailer

import (
	"crypto/md5"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/smtp"
	"net/textproto"
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

type Mailer struct {
	tpl        *sweetpl.SweeTpl
	config     *Config
	tlsConfig  *tls.Config
	simpleAuth smtp.Auth
	db         *sql.DB
}

func GetMailer(config *Config) (*Mailer, error) {

	simpleAuth := smtp.PlainAuth("", config.SMTP.Username, config.SMTP.Password, config.SMTP.Hello)

	tlsConfig := &tls.Config{
		ServerName: config.SMTP.Hello,
	}

	db, err := sql.Open("mysql", config.DSN)
	if err != nil {
		return nil, err
	}

	tpl := &sweetpl.SweeTpl{
		Loader: &sweetpl.DirLoader{
			BasePath: config.TemplatePath,
		},
	}

	return &Mailer{
		tpl:        tpl,
		config:     config,
		tlsConfig:  tlsConfig,
		simpleAuth: simpleAuth,
		db:         db,
	}, nil
}

func (m *Mailer) getSmtpClient() (*smtp.Client, error) {

	log.Printf("Dial %s\n", m.config.SMTP.Server)
	smtpClient, err := smtp.Dial(m.config.SMTP.Server)
	if err != nil {
		return nil, err
	}
	if err := smtpClient.Hello(m.config.SMTP.Hello); err != nil {
		return nil, err
	}
	log.Println("STARTTLS")
	if err := smtpClient.StartTLS(m.tlsConfig); err != nil {
		return nil, err
	}
	if err := smtpClient.Auth(m.simpleAuth); err != nil {
		return nil, fmt.Errorf("AUTH: %s", err.Error())
	}
	log.Println("SMTP Client Connected")
	return smtpClient, nil
}

func (m *Mailer) DoMailLoop() error {

	res, err := m.db.Query(fmt.Sprintf(`
	SELECT id, first, last, email 
	FROM %s
	WHERE send1 IS NULL
	AND unsubscribe IS NULL
	AND fail IS NULL`, m.config.Table))

	if err != nil {
		return err
	}

	smtpClient, err := m.getSmtpClient()
	if err != nil {
		return fmt.Errorf("Creating SMTP Client: %s", err.Error())
	}

	var numberSent int = 0
	for res.Next() {

		if numberSent > 10 {
			// Establish new smtp client for every 10 emails. Just in case.
			if err := smtpClient.Quit(); err != nil {
				return fmt.Errorf("Quit: %s", err.Error())
			}
			smtpClient, err = m.getSmtpClient()
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
		_, err = m.db.Exec(fmt.Sprintf(`UPDATE %s SET send1 = 1 WHERE id = ?`, m.config.Table), data.ID)
		if err != nil {
			log.Println(err.Error())
			continue
		}

		err = m.sendEmail(smtpClient, data)
		if err != nil {
			log.Println(err.Error())
			continue
		}

		_, err = m.db.Exec(fmt.Sprintf(`UPDATE %s SET send1 = 2 WHERE id = ?`, m.config.Table), data.ID)
		if err != nil {
			log.Println(err.Error())
			continue
		}
	}

	smtpClient.Quit()
	smtpClient.Close()
	return nil
}

func (m *Mailer) sendEmail(smtpClient *smtp.Client, data *EmailData) error {

	if err := smtpClient.Reset(); err != nil {
		return err
	}

	if data.Email == nil {
		return fmt.Errorf("NO Email address for %d", data.ID)
	}

	log.Printf("SEND TO %s\n", *data.Email)

	if err := smtpClient.Mail(m.config.SMTP.From); err != nil {
		return err
	}
	if err := smtpClient.Rcpt(*data.Email); err != nil {
		return err
	}
	writer, err := smtpClient.Data()
	if err != nil {
		return err
	}

	err = m.writeEmail(writer, data) //writer, data)
	if err != nil {
		return err
	}

	err = writer.Close()
	if err != nil {
		return err
	}
	return nil
}

func (m *Mailer) writeEmail(w io.Writer, data *EmailData) error {

	var err error

	mw := multipart.NewWriter(w)

	unsubscribe := m.config.ListUnsubscribe
	if strings.Contains(unsubscribe, "%s") {
		hashWriter := md5.New()
		fmt.Fprintf(hashWriter, "%s%d%s", *data.Email, data.ID, m.config.UnsubscribeSecret)
		b64e := base64.URLEncoding.EncodeToString([]byte(*data.Email))
		b64e += "-"
		b64e += base64.URLEncoding.EncodeToString(hashWriter.Sum(nil))
		unsubscribe = fmt.Sprintf(unsubscribe, b64e)
	}
	headers := map[string]string{
		"From":             m.config.From, //`"OSMAD" <info@osmad.com.au>`,
		"To":               *data.Email,
		"Subject":          m.config.Subject,
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

	err = m.tpl.Render(textPart, m.config.TextTemplate, data)
	if err != nil {
		return err
	}

	hdr2 := textproto.MIMEHeader{}
	hdr2.Add("Content-Type", "text/html")
	htmlPart, err := mw.CreatePart(hdr2)
	if err != nil {
		return err
	}

	err = m.tpl.Render(htmlPart, m.config.EmailTemplate, data)
	if err != nil {
		return err
	}
	mw.Close()
	return nil
}
