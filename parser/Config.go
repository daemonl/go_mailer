package parser

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
