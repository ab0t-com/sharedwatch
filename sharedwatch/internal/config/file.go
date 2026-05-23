package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

func Load(path string, base Config) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return base, err
	}
	defer f.Close()

	cfg := base
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		switch key {
		case "watch_path":
			cfg.WatchPath = val
		case "data_dir":
			cfg.DataDir = val
		case "db_path":
			cfg.DBPath = val
		case "recursive":
			cfg.Recursive = strings.EqualFold(val, "true")
		case "coalesce_window":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.CoalesceWindow = d
			}
		case "passive_interval":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.PassiveInterval = d
			}
		case "active_interval":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.ActiveInterval = d
			}
		case "active_ttl":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.ActiveTTL = d
			}
		case "reconcile_interval":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.ReconcileInterval = d
			}
		case "max_batch_size":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.MaxBatchSize = n
			}
		case "ignore_patterns":
			cfg.IgnorePatterns = splitCSV(val)
		case "retention_days":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.RetentionDays = n
			}
		}
	}
	return cfg, s.Err()
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
