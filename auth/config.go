package imqsauth

import (
	"fmt"
	"os"
	"strings"

	"github.com/IMQS/authaus"
	"github.com/IMQS/log"
	serviceconfig "github.com/IMQS/serviceconfigsgo"
)

const (
	serviceConfigFileName = "imqsauth.json"
	serviceConfigVersion  = 1
	serviceName           = "ImqsAuth"
)

type ConfigYellowfin struct {
	Enabled bool
	// Filter YF categories according to current IMQS module, which is passed in from the front-end.
	ContentCategoryFilter bool
	// Map IMQS modules to Yellowfin report categories for cases where it does not match, e.g. Water Demand->Swift.
	ModuleToCategoryMapping map[string]string
	// Pass in the IMQS scenario as a field used to filter reports.
	SourceAccessFilter bool
}

// Note: Be sure to keep doc.go up to date with the Config structure here

type Config struct {
	Authaus                    authaus.Config
	Yellowfin                  ConfigYellowfin
	PasswordResetExpirySeconds float64
	NewAccountExpirySeconds    float64
	SendMailPassword           string // NB: When moving SendMailPassword to a standalone secrets file, change for PCS also. PCS reads imqsauth config file.
	NotificationUrl            string
	hostname                   string // This is read from environment variable the first time GetHostname is called
	lastFileLoaded             string // Used for relative paths (such as HostnameFile)
	enablePcsRename            bool   // Disabled by unit tests
}

func (x *Config) Reset() {
	*x = Config{}
	x.PasswordResetExpirySeconds = 24 * 3600
	x.NewAccountExpirySeconds = 5 * 365 * 24 * 3600
	x.enablePcsRename = true
	x.Authaus.Reset()
}

// Performs setup specific to unit tests
func (x *Config) ResetForUnitTests() {
	x.Reset()
	x.enablePcsRename = false
}

func (x *Config) LoadFile(filename string) error {
	x.Reset()
	err := serviceconfig.GetConfig(filename, serviceName, serviceConfigVersion, serviceConfigFileName, x)
	if err != nil {
		return err
	}
	x.lastFileLoaded = filename
	return nil
}

func (x *Config) IsContainer() bool {
	return serviceconfig.IsContainer()
}

func (x *Config) GetHostname() string {
	if x.hostname == "" {
		hostname_b, ok := os.LookupEnv("IMQS_HOSTNAME_URL")
		if ok {
			x.hostname = strings.TrimSpace(string(hostname_b))
		}
	}
	return x.hostname
}

// MakeOutsideDocker changes all of the hostnames from our common hostnames in
// docker-compose files, to 'localhost'. This is built to allow a developer to
// debug the Auth service, while running everything else in docker.
func (x *Config) MakeOutsideDocker() {
	fmt.Printf("OutsideDocker changes: db => localhost, port => 2003, IMQS_HOSTNAME_URL => http://localhost:2500\n")
	translateDB := func(db *authaus.DBConnection) {
		if db.Host == "db" {
			db.Host = "localhost"
			// db.Port = 6432 // DO NOT COMMIT (for testing PgBouncer)
		}
	}
	translateDB(&x.Authaus.DB)
	if x.Authaus.HTTP.Port == "80" {
		x.Authaus.HTTP.Port = "2003"
	}
	x.Authaus.Log.Filename = log.Stdout
	x.hostname = "http://localhost:2500"
}
