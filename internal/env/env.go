package env

import "os"

type EnvConfig struct {
	ServerURL          string
	DatabaseURL        string
	ClientIDGoogle     string
	ClientSecretGoogle string
	//ClientIDApple		string
	//ClientSecretApple	string
	//...

}

func LoadEnvConfig() EnvConfig {
	cfg := EnvConfig{
		ServerURL:          os.Getenv("SERVER_URL"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		ClientIDGoogle:     os.Getenv("GOOGLE_OAUTH2_CLIENT_ID"),
		ClientSecretGoogle: os.Getenv("GOOGLE_OAUTH2_CLIENT_SECRET"),
		//clientIDApple:	 os.Getenv("APPLE_OAUTH2_CLIENT_ID"),
		//clientSecretApple: os.Getenv("APPLE_OAUTH2_CLIENT_SECRET"),
		//...
	}

	return cfg
}
