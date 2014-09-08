package mailer

type Config struct {
	TemplatePath      string `json:"templateRoot"`
	EmailTemplate     string `json:"htmlTemplate"`
	TextTemplate      string `json:"textTemplate"`
	MailServer        string `json:"mailServer"`
	DSN               string `json:"dsn"`
	Table             string `json:"recipientTable"`
	Subject           string `json:"subject"`
	From              string `json:"from"`
	UnsubscribeSecret string `json:"unsubscribeSecret"`
	ListUnsubscribe   string `json:"listUnsubscribe"`
	SMTP              struct {
		Server   string `json:"server"`
		Hello    string `json:"hello"`
		From     string `json:"from"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"smtp"`
}
