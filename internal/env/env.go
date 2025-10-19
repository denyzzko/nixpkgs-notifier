package env

import (
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/joho/godotenv"
)

type EnvConfig struct {
	ServerURL          string
	DatabaseURL        string
	ClientIDGoogle     string
	ClientSecretGoogle string
	//ClientIDApple		string
	//ClientSecretApple	string
	//...

}

func LoadEnvConfig() (*EnvConfig, error) {

	// load .env file from root of this project
	err := godotenv.Load()
	if err != nil {
		return nil, err
	}

	// build db url
	dbUrl := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASS")),
		Host:     net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT")),
		Path:     os.Getenv("DB_NAME"),
		RawQuery: "sslmode=" + url.QueryEscape(os.Getenv("DB_SSLMODE")),
	}

	fmt.Println(dbUrl.String())

	cfg := &EnvConfig{
		ServerURL:          os.Getenv("SERVER_URL"),
		DatabaseURL:        dbUrl.String(),
		ClientIDGoogle:     os.Getenv("GOOGLE_OAUTH2_CLIENT_ID"),
		ClientSecretGoogle: os.Getenv("GOOGLE_OAUTH2_CLIENT_SECRET"),
		//clientIDApple:	 os.Getenv("APPLE_OAUTH2_CLIENT_ID"),
		//clientSecretApple: os.Getenv("APPLE_OAUTH2_CLIENT_SECRET"),
		//...
	}

	return cfg, nil
}
