package main

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
)

// printProgress writes a clean, fixed-width progress line to stdout.
func printProgress(service, status string) {
	fmt.Printf("  %-14s %s\n", status, service)
}

// downProgress returns a Progress func that strips the stack name prefix
// from container names before printing (e.g. "myapp_web" → "web").
func downProgress(stackName string) dag.Progress {
	return func(name, status string) {
		display := strings.TrimPrefix(name, stackName+"_")
		printProgress(display, status)
	}
}

var stackAdjectives = []string{
	"autumn", "bold", "brave", "bright", "calm", "clever", "cold", "cool",
	"crisp", "dark", "deft", "eager", "early", "faint", "fast", "firm",
	"fleet", "glad", "golden", "grand", "happy", "hardy", "heavy", "hidden",
	"icy", "idle", "keen", "kind", "lofty", "loud", "lucky", "merry",
	"mild", "misty", "neat", "noble", "odd", "pale", "plain", "proud",
	"quick", "quiet", "rapid", "rare", "rough", "round", "royal", "rusty",
	"sandy", "sharp", "shiny", "silent", "silver", "slim", "smart", "snowy",
	"soft", "solid", "speedy", "stout", "strong", "sunny", "swift", "tall",
	"tame", "tiny", "tough", "true", "vast", "warm", "wild", "wise",
}

var stackAnimals = []string{
	"ant", "ape", "bear", "bee", "bird", "boar", "buck", "bull",
	"cat", "clam", "crab", "crow", "deer", "dog", "dove", "duck",
	"eagle", "elk", "fish", "flea", "fly", "frog", "gnu", "goat",
	"hawk", "hen", "hog", "ibis", "jay", "kite", "lamb", "lark",
	"lion", "lynx", "mink", "mole", "moth", "mule", "newt", "owl",
	"ox", "pike", "pony", "puma", "quail", "ram", "rat", "rook",
	"seal", "shrew", "slug", "snail", "snake", "stag", "swan", "toad",
	"trout", "vole", "wasp", "wolf", "worm", "wren", "yak", "zebra",
}

// resolveStackName returns cfg.Name when set, otherwise derives a deterministic
// adjective-animal name from the absolute path of the config file.
func resolveStackName(cfg *config.DillConfig, configFile string) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	return derivedStackName(configFile)
}

func derivedStackName(configFile string) string {
	abs, err := filepath.Abs(configFile)
	if err != nil {
		abs = configFile
	}
	h := fnv.New32a()
	h.Write([]byte(abs))
	sum := h.Sum32()
	adj := stackAdjectives[sum%uint32(len(stackAdjectives))]
	ani := stackAnimals[(sum/uint32(len(stackAdjectives)))%uint32(len(stackAnimals))]
	return adj + "-" + ani
}

// loadEnvFile reads KEY=VALUE pairs from path and sets them as environment
// variables, skipping keys that are already set. Blank lines and lines
// beginning with # are ignored. Surrounding single or double quotes are
// stripped from values.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening env file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}
