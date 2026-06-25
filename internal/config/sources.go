package config

import (
	"errors"
	"flag"
	"os"
	"strings"
)

func ConfigFromEnv() (Config, error) {
	return ConfigFromMap(envMap(os.Environ()))
}

func ConfigFromMap(env map[string]string) (Config, error) {
	values := defaultConfigValues()
	if err := values.applyEnv(env); err != nil {
		return Config{}, err
	}
	return values.config()
}

func ConfigFromSources(args []string, env []string) (Config, error) {
	return configFromSources(args, env, DefaultConfigPath)
}

func configFromSources(args []string, env []string, defaultConfigPath string) (Config, error) {
	cli, err := parseConfigFlags(args)
	if err != nil {
		return Config{}, err
	}
	if cli.help {
		return Config{}, flag.ErrHelp
	}

	values := defaultConfigValues()

	configPath := defaultConfigPath
	explicitConfig := cli.configPath != nil
	if cli.configPath != nil {
		configPath = strings.TrimSpace(*cli.configPath)
	}
	if configPath != "" {
		raw, err := configValuesFromTOMLFile(configPath)
		if err != nil {
			if !explicitConfig && errors.Is(err, os.ErrNotExist) {
				// The default config file is optional so env-only and CLI-only runs keep working.
			} else {
				return Config{}, err
			}
		} else {
			values.applyRaw(raw)
		}
	}

	if err := values.applyEnv(envMap(env)); err != nil {
		return Config{}, err
	}
	values.applyRaw(cli.rawConfig)
	return values.config()
}
