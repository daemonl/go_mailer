package parser

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"log"
	"regexp"
	"strings"

	"code.google.com/p/goauth2/oauth"
	"code.google.com/p/google-api-go-client/gmail/v1"
	"github.com/daemonl/goauthcli"
	_ "github.com/go-sql-driver/mysql"
)

type Parser struct {
	db     *sql.DB
	sv     *gmail.Service
	config *Config
}

func GetParser(config *Config) (*Parser, error) {

	oauthConfig := &oauth.Config{
		ClientId:     config.OAuth.ClientID,
		ClientSecret: config.OAuth.ClientSecret,
		RedirectURL:  config.OAuth.RedirectURL,
		Scope:        gmail.GmailModifyScope,
		AccessType:   "offline",
		AuthURL:      "https://accounts.google.com/o/oauth2/auth",
		TokenURL:     "https://accounts.google.com/o/oauth2/token",
		TokenCache:   oauth.CacheFile(config.OAuth.TokenFile),
	}

	db, err := sql.Open("mysql", config.DSN)
	if err != nil {
		return nil, err
	}

	transport, err := goauthcli.GetTransport(oauthConfig, config.ServerBind)
	if err != nil {
		return nil, err
	}

	sv, err := gmail.New(transport.Client())
	if err != nil {
		return nil, err
	}

	return &Parser{
		db:     db,
		sv:     sv,
		config: config,
	}, nil
}

func (p *Parser) ListLabels() error {
	labels, err := p.sv.Users.Labels.List(p.config.UserID).Do()
	if err != nil {
		return err
	}
	for _, label := range labels.Labels {
		fmt.Printf("%s: %s\n", label.Id, label.Name)
	}
	return nil
}

func (p *Parser) ParseSubscribes() error {
	req := p.sv.Users.Messages.List(p.config.UserID).LabelIds("INBOX").Q("subject:PRESENTER OPT IN EMAIL MESSAGE SUBJECT")
	err := p.GetMessages(req, p.HandleSubscribe)
	if err != nil {
		return err
	}
	return nil
}

func (p *Parser) ParseUnsubscribes() error {
	req := p.sv.Users.Messages.List(p.config.UserID).LabelIds("INBOX").Q("subject:unsubscribe")
	err := p.GetMessages(req, p.HandleUnsubscribe)
	if err != nil {
		return err
	}
	return nil
}

func (p *Parser) ParseFailures() error {

	req := p.sv.Users.Messages.List(p.config.UserID).LabelIds("INBOX").Q(`subject:("Undeliverable" OR "(Failure)" OR "Returned Mail")`)
	err := p.GetMessages(req, p.HandleDeliveryStatus)
	if err != nil {
		return err
	}
	return nil
}

var reSentTo *regexp.Regexp = regexp.MustCompile(`This email was sent to [^\(]*\(([^@]*@[^\)< ]*)`)
var reStatusAction *regexp.Regexp = regexp.MustCompile(`[Aa]ction: ([a-z]*)`)
var reStatusRecipient *regexp.Regexp = regexp.MustCompile(`[fF]inal[-_][rR]ecipient:[^;]*; ?<?([^@]*@[a-zA-Z0-9\-\.\_]*)`)
var reQmailFailure *regexp.Regexp = regexp.MustCompile(`^<([^@]*@[a-zA-Z0-9\-\.\_]*)>:`)

func (p *Parser) HandleSubscribe(message *gmail.Message) error {
	body, err := getMimePartString(message.Payload, "text/plain")
	if err != nil {
		return err
	}
	if len(body) < 1 {
		return fmt.Errorf("No text/plain in subscribe message")
	}
	lines := strings.Split(body, "\n")
	name := ""
	email := ""
	for _, line := range lines {
		line := strings.TrimSpace(line)
		if strings.HasPrefix(line, "Name") {
			name = line[7:]
			continue
		}
		if strings.HasPrefix(line, "Email") {
			email = line[8:]
			continue
		}

	}

	nameParts := strings.SplitN(name, " ", 2)
	if len(nameParts) < 2 {
		err = p.Subscribe(email, name, "")
	} else {
		err = p.Subscribe(email, nameParts[0], nameParts[1])
	}
	if err != nil {
		if strings.HasPrefix(err.Error(), "Error 1062") {
			fmt.Println("Duplicate, Skip")
		} else {
			return err
		}
	}
	p.MoveMessage(message, "INBOX", p.config.Labels["subscribe"])
	return nil
}

func (p *Parser) HandleUnsubscribe(message *gmail.Message) error {

	body, err := getMimePartString(message.Payload, "text/plain")
	if err != nil {
		return err
	}
	if len(body) < 1 {
		fmt.Println("No plaintext body, try html")
		body, err := getMimePartString(message.Payload, "text/html")
		if err != nil {
			return err
		}
		if len(body) < 1 {
			return fmt.Errorf("Message %s had no plaintext or html body\n", message.Id)
		}
	}
	body = reReplyNewline.ReplaceAllString(body, "")
	sentTo := reSentTo.FindStringSubmatch(body)
	if len(sentTo) > 0 {
		err = p.Unsubscribe(sentTo[1])
		if err != nil {
			return err
		}
		p.MoveMessage(message, "INBOX", p.config.Labels["unsubscribe"])
		return nil
	} else {
		return fmt.Errorf("Message %s had no parseable address\n", message.Id)
	}
	return nil
}

func (p *Parser) MoveMessage(m *gmail.Message, from string, to string) {
	//log.Println("NOT MOVING MESSAGE")
	//return

	req := &gmail.ModifyMessageRequest{
		RemoveLabelIds: []string{from},
		AddLabelIds:    []string{to},
	}
	_, err := p.sv.Users.Messages.Modify(p.config.UserID, m.Id, req).Do()
	if err != nil {
		fmt.Printf("Could not move message %s: %s\n", m.Id, err.Error())
	} else {
		fmt.Printf("Moved message %s from %s to %s\n", m.Id, from, to)
	}
}

func (p *Parser) findHeader(m *gmail.MessagePart, name string) (string, bool) {
	name = strings.ToLower(name)
	for _, h := range m.Headers {
		if name == strings.ToLower(h.Name) {
			return h.Value, true
		}
		//fmt.Printf("%s != %s\n", h.Name, name)
	}
	return "", false
}

func (p *Parser) HandleDeliveryStatus(m *gmail.Message) error {

	addr := ""
	if addr = p.tryXHeader(m); len(addr) > 0 {
	} else if addr = p.tryDeliveryStatus(m); len(addr) > 0 {
	} else if addr = p.tryPlaintext(m); len(addr) > 0 {
	} else {
		return fmt.Errorf("No method found an undeliverable address")
	}

	p.MoveMessage(m, "INBOX", p.config.Labels["undeliverable"])
	err := p.Undeliverable(addr)
	if err != nil {
		return err
	}
	return nil
}

func (p *Parser) tryDeliveryStatus(m *gmail.Message) string {
	payload, err := getMimePartFromPart(m.Payload, "message/delivery-status")
	if err != nil {
		log.Println(err.Error)
		return ""
	}
	log.Println("GOT ACTION DELIVERY STATUS")
	plainTextPart, err := getMimePartString(payload, "text/plain")
	if err != nil {
		log.Println(err.Error)
		return ""
	}
	recipient := reStatusRecipient.FindStringSubmatch(plainTextPart)
	if len(recipient) < 2 {
		return ""
	}
	return recipient[1]
}

func (p *Parser) tryPlaintext(m *gmail.Message) string {

	body, err := getMimePartString(m.Payload, "text/plain")
	if err != nil {
		fmt.Println(err.Error())
		return ""
	}

	if strings.HasPrefix(body, `Hi. This is the qmail-send program at`) {
		fmt.Println("QMAIL")
		addr := reQmailFailure.FindStringSubmatch(body)
		if len(addr) > 1 {
			return addr[1]
		}
	}
	return ""
}
func (p *Parser) tryXHeader(m *gmail.Message) string {

	if failHeader, ok := p.findHeader(m.Payload, "X-Failed-Recipients"); ok {
		return failHeader
	}
	return ""
}

func getMimePartFromPart(payload *gmail.MessagePart, mimeType string) (*gmail.MessagePart, error) {
	if payload == nil {
		return nil, nil
	}
	if payload.MimeType == mimeType {
		return payload, nil
	}
	for _, part := range payload.Parts {
		rp, err := getMimePartFromPart(part, mimeType)
		if err != nil {
			return nil, err
		}
		if rp != nil {
			return rp, nil
		}
	}
	return nil, nil

}

func getMimePartString(msgPayload *gmail.MessagePart, mimeType string) (string, error) {
	payload, err := getMimePartFromPart(msgPayload, mimeType)
	if err != nil {
		return "", err
	}
	if payload == nil {
		return "", nil
	}
	bodyBytes, err := base64.URLEncoding.DecodeString(payload.Body.Data)
	return string(bodyBytes), err
}

var reReplyNewline *regexp.Regexp = regexp.MustCompile(`\n>[ ]*`)

func (p *Parser) GetMessages(listRequest *gmail.UsersMessagesListCall, msgCallback func(*gmail.Message) error) error {

	resp, err := listRequest.Do()
	if err != nil {
		return err
	}
	for {
		for _, mHeader := range resp.Messages {
			message, err := p.sv.Users.Messages.Get(p.config.UserID, mHeader.Id).Format("full").Do()
			if err != nil {
				return err
			}
			fmt.Printf("GOT MESSAGE %s\n%s\n", message.Id, message.Snippet)
			err = msgCallback(message)

			if err != nil {
				fmt.Println(err.Error())
			}
		}
		if len(resp.NextPageToken) < 1 {
			break
		}
		newReq := listRequest.PageToken(resp.NextPageToken)
		resp, err = newReq.Do()
		//sv.Users.Messages.List(p.config.UserID).LabelIds(label).PageToken(resp.NextPageToken).Do()
		if err != nil {
			return err
		}
	}

	return nil

}
