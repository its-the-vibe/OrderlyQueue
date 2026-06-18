package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Redis struct {
		Addr     string `yaml:"addr"`
		Password string `yaml:"password"`
		DB       int    `yaml:"db"`
	} `yaml:"redis"`
	Keys struct {
		Lock           string `yaml:"lock"`
		PRList         string `yaml:"pr_list"`
		PoppitList     string `yaml:"poppit_list"`
		MergeCommitSHA string `yaml:"merge_commit_sha"`
	} `yaml:"keys"`
	Channels struct {
		GithubEvents string `yaml:"github_events"`
		CICDEvents   string `yaml:"cicd_events"`
	} `yaml:"channels"`
	Timeouts struct {
		LockSleep  time.Duration `yaml:"lock_sleep"`
		LockExpiry time.Duration `yaml:"lock_expiry"`
		CICDDelay  time.Duration `yaml:"cicd_delay"`
	} `yaml:"timeouts"`
	Poppit struct {
		Dir string `yaml:"dir"`
	} `yaml:"poppit"`
}

func LoadConfig(path string) (*Config, error) {
	_ = godotenv.Load()

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg := &Config{}
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(cfg); err != nil {
		return nil, err
	}

	if pw := os.Getenv("REDIS_PASSWORD"); pw != "" {
		cfg.Redis.Password = pw
	}

	return cfg, nil
}
