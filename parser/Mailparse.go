package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"

	"code.google.com/p/goauth2/oauth"
	"code.google.com/p/google-api-go-client/gmail/v1"
	"github.com/daemonl/goauthcli"
	_ "github.com/go-sql-driver/mysql"
)

var configFilename string

var db *sql.DB
var sv *gmail.Service
var userId string
var labels map[string]string

func init() {
	flag.StringVar(&configFilename, "config", "config.json", "The filename to load json config from")
}

type Config struct {
	OAuth struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		RedirectURL  string `json:"redirectURL"`
		TokenFile    string `json:"tokenFile"`
	} `json:"oauth"`
	UserID     string            `json:"userId"`
	DSN        string            `json:"dsn"`
	Labels     map[string]string `json:"labels"`
	ServerBind string            `json:"serverBind"`
	Table      string            `json:"recipientTable"`
}

func setup() error {
	flag.Parse()

	var configReader io.Reader
	var err error

	config := &Config{}

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

	userId = config.UserID
	labels = config.Labels

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

	db, err = sql.Open("mysql", config.DSN)
	if err != nil {
		return err
	}

	transport, err := goauthcli.GetTransport(oauthConfig, config.ServerBind)

	sv, err = gmail.New(transport.Client())
	if err != nil {
		return err
	}

	return nil
}

func main() {

	err := setup()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	labels, err := sv.Users.Labels.List(userId).Do()
	if err != nil {
		fmt.Printf("Error authenticating: %s\n", err.Error())
		os.Exit(1)
	}
	for _, label := range labels.Labels {
		fmt.Printf("%s: %s\n", label.Id, label.Name)
	}

	fmt.Printf("Unsubscribe code: %s\n", unique)

	req := sv.Users.Messages.List(userId).LabelIds("INBOX").Q("subject:unsubscribe")
	err = getMessages(req, parseUnsubscribe)
	if err != nil {
		fmt.Printf("Error fetching: %s\n", err.Error())
		os.Exit(1)
	}

	/*
		req := sv.Users.Messages.List(userId).LabelIds("INBOX")
		err = getMessages(req, parseDeliveryFail)
		if err != nil {
			fmt.Printf("Error fetching: %s\n", err.Error())
			os.Exit(1)
		}*/
}

var reSentTo *regexp.Regexp = regexp.MustCompile(`This email was sent to [^\(]*\(([^@]*@[^\)< ]*)`)
var reStatusAction *regexp.Regexp = regexp.MustCompile(`[Aa]ction: ([a-z]*)`)
var reStatusRecipient *regexp.Regexp = regexp.MustCompile(`[fF]inal[-_][rR]ecipient:[^;]*; ?<?([^@]*@[a-zA-Z0-9\-\.\_]*)`)

func parseUnsubscribe(message *gmail.Message) error {

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
		err = unsubscribe(sentTo[1])
		if err != nil {
			return err
		}
		moveMessage(message, "INBOX", labels["unsubscribe"])
		return nil
	} else {
		return fmt.Errorf("Message %s had no parseable address\n", message.Id)
	}
	return nil
}

func moveMessage(m *gmail.Message, from string, to string) {
	req := &gmail.ModifyMessageRequest{
		RemoveLabelIds: []string{from},
		AddLabelIds:    []string{to},
	}
	_, err := sv.Users.Messages.Modify(userId, m.Id, req).Do()
	if err != nil {
		fmt.Printf("Could not move message %s: %s\n", m.Id, err.Error())
	} else {
		fmt.Printf("Moved message %s from %s to %s\n", m.Id, from, to)
	}
}

func parseDeliveryFail(m *gmail.Message) error {
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
		moveMessage(m, "INBOX", labels["undeliverable"])
		err = undeliverable(recipient[1])
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

func getMessages(listRequest *gmail.UsersMessagesListCall, msgCallback func(*gmail.Message) error) error {

	//req := sv.Users.Messages.List(userId)
	//.LabelIds(label)

	resp, err := listRequest.Do()
	if err != nil {
		return err
	}
	for {
		for _, mHeader := range resp.Messages {
			message, err := sv.Users.Messages.Get(userId, mHeader.Id).Format("full").Do()
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
		//sv.Users.Messages.List(userId).LabelIds(label).PageToken(resp.NextPageToken).Do()
		if err != nil {
			return err
		}
	}

	return nil

}
