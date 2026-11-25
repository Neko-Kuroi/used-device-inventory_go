// Package cli resolves the program's configuration and dispatches
// its subcommands: serve (the web app), list-users and
// reset-password (the host-side user commands).
package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"used-device-inventory-go/internal/auth"
	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/web"
)

// Config is the resolved configuration. The resolution order is the
// design's: a command-line flag wins over the environment, which
// wins over config.txt beside the executable, which wins over the
// built-in default.
type Config struct {
	BaseDir string // directory of the executable
	DBPath  string // DEVICE_DB, else <BaseDir>/instance/devices.db
	Addr    string // --addr, else DEVICE_ADDR, else config.txt, else 127.0.0.1:5000
}

const DefaultAddr = "127.0.0.1:5000"

// readConfigFile parses <baseDir>/config.txt ("key=value" lines,
// blank lines and # comments ignored). A missing file is not an
// error; it simply provides no values.
func readConfigFile(baseDir string) map[string]string {
	values := map[string]string{}
	data, err := os.ReadFile(filepath.Join(baseDir, "config.txt"))
	if err != nil {
		return values
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(val)
		}
	}
	return values
}

// Resolve computes the Config for a serve invocation. flagAddr is
// the --addr value, or "" when the flag was not given.
func Resolve(baseDir, flagAddr string) Config {
	cfg := Config{BaseDir: baseDir}
	if v := os.Getenv("DEVICE_DB"); v != "" {
		cfg.DBPath = v
	} else {
		cfg.DBPath = filepath.Join(baseDir, "instance", "devices.db")
	}
	fileValues := readConfigFile(baseDir)
	switch {
	case flagAddr != "":
		cfg.Addr = flagAddr
	case os.Getenv("DEVICE_ADDR") != "":
		cfg.Addr = os.Getenv("DEVICE_ADDR")
	case fileValues["addr"] != "":
		cfg.Addr = fileValues["addr"]
	default:
		cfg.Addr = DefaultAddr
	}
	return cfg
}

// InstanceDir is the directory holding the database, photos and the
// secret key: the directory of the database file.
func (c Config) InstanceDir() string { return filepath.Dir(c.DBPath) }

const usage = `usage: inventory [command]

commands:
  serve [--addr host:port]   start the server (default command)
  list-users                 list users
  reset-password <username>  reset a password from this machine
  help                       show this text
`

// Run executes one CLI invocation and returns the process exit code.
func Run(args []string, baseDir string, stdout, stderr io.Writer) int {
	command := "serve"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		rest = args[1:]
	}
	switch command {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "serve":
		return runServe(rest, baseDir, stdout, stderr)
	case "list-users":
		return runListUsers(baseDir, stdout, stderr)
	case "reset-password":
		return runResetPassword(rest, baseDir, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n%s", command, usage)
		return 1
	}
}

func parseAddrFlag(args []string) (string, error) {
	addr := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--addr" && i+1 < len(args):
			addr = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--addr="):
			addr = strings.TrimPrefix(args[i], "--addr=")
		default:
			return "", fmt.Errorf("unknown serve argument: %s", args[i])
		}
	}
	return addr, nil
}

// Prepare performs startup preparation: resolve the configuration,
// create the instance directory, open and initialise the database,
// and load (or create) the secret key.
func Prepare(baseDir, flagAddr string) (Config, *sql.DB, string, error) {
	cfg := Resolve(baseDir, flagAddr)
	if err := os.MkdirAll(cfg.InstanceDir(), 0o755); err != nil {
		return cfg, nil, "", err
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return cfg, nil, "", err
	}
	ctx := context.Background()
	if err := db.Init(ctx, database); err != nil {
		database.Close()
		return cfg, nil, "", err
	}
	secretKey, err := auth.LoadOrCreateSecretKey(cfg.InstanceDir())
	if err != nil {
		database.Close()
		return cfg, nil, "", err
	}
	return cfg, database, secretKey, nil
}

// runServe prepares and then serves HTTP until the process ends.
func runServe(args []string, baseDir string, stdout, stderr io.Writer) int {
	flagAddr, err := parseAddrFlag(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cfg, database, secretKey, err := Prepare(baseDir, flagAddr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer database.Close()
	version, err := db.UserVersion(context.Background(), database)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	server, err := web.NewServer(database, secretKey, cfg.InstanceDir())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "ready: addr=%s db=%s (user_version=%d)\n",
		cfg.Addr, cfg.DBPath, version)
	fmt.Fprintf(stdout, "listening on http://%s/ (Ctrl-C to stop)\n", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, server); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
