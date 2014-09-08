package parser

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"regexp"

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

func (p *Parser) ParseUnsubscribes() error {
	req := p.sv.Users.Messages.List(p.config.UserID).LabelIds("INBOX").Q("subject:unsubscribe")
	err := p.GetMessages(req, p.HandleUnsubscribe)
	if err != nil {
		return err
	}
	return nil
}

func (p *Parser) ParseFailures() error {

	req := p.sv.Users.Messages.List(p.config.UserID).LabelIds("INBOX")
	err := p.GetMessages(req, p.HandleDeliveryStatus)
	if err != nil {
		return err
	}
	return nil
}

var reSentTo *regexp.Regexp = regexp.MustCompile(`This email was sent to [^\(]*\(([^@]*@[^\)< ]*)`)
var reStatusAction *regexp.Regexp = regexp.MustCompile(`[Aa]ction: ([a-z]*)`)
var reStatusRecipient *regexp.Regexp = regexp.MustCompile(`[fF]inal[-_][rR]ecipient:[^;]*; ?<?([^@]*@[a-zA-Z0-9\-\.\_]*)`)

func (p *Parser) HandleUnsubscribe(message *gmail.Message) error {

	body, err := getMimePartString(message, "text/plain")
	if err != nil {
		return err
	}
	if len(body) < 1 {
		fmt.Println("No plaintext body, try html")
		body, err := getMimePartString(message, "text/html")
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

func (p *Parser) HandleDeliveryStatus(m *gmail.Message) error {
	body, err := getMimePartString(m, "message/delivery-status")
	if err != nil {
		return err
	}
	if len(body) < 1 {
		return fmt.Errorf("No delivery status part")
	}
	action := reStatusAction.FindStringSubmatch(body)
	recipient := reStatusRecipient.FindStringSubmatch(body)
	if len(action) == 0 || len(recipient) == 0 {
		fmt.Println("PARSE FAIL ::::::::")
		fmt.Println(body)
		fmt.Println("PARSE FAIL ========")
		return fmt.Errorf("=================================")
	}
	if action[1] == "failed" {
		p.MoveMessage(m, "INBOX", p.config.Labels["undeliverable"])
		err = p.Undeliverable(recipient[1])
		if err != nil {
			return err
		}
	}
	return nil
}

func getMimePartString(message *gmail.Message, mimeType string) (string, error) {
	if message.Payload.MimeType == mimeType {
		bodyBytes, err := base64.URLEncoding.DecodeString(message.Payload.Body.Data)
		return string(bodyBytes), err
	}
	for _, part := range message.Payload.Parts {
		if part.MimeType == mimeType {
			if len(part.Parts) > 0 {
				part = part.Parts[0]
			}
			bodyBytes, err := base64.URLEncoding.DecodeString(part.Body.Data)
			return string(bodyBytes), err
		}
	}
	return "", nil
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
			fmt.Printf("GOT MESSAGE %s\n", message.Id)
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
