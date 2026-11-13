package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/suttonwilliamd/pebble/internal/repository"
)

const maxConfigBytes = 64 << 10

func identityKey(key string) bool {
	switch key {
	case "user.name", "user.email", "author.name", "author.email", "committer.name", "committer.email":
		return true
	}
	return false
}
func validIdentityValue(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 65535 && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
func globalConfigPath() (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".pebbleconfig"), nil
}
func readIdentityConfig(path string) (_ map[string]string, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("identity config is not an ordinary file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("identity config changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigBytes {
		return nil, fmt.Errorf("identity config exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("invalid identity config: %s", path)
	}
	values := map[string]string{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || !identityKey(key) {
			return nil, fmt.Errorf("unknown identity config key: %v", token)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("duplicate identity config key: %s", key)
		}
		var value string
		if err = decoder.Decode(&value); err != nil {
			return nil, err
		}
		if !validIdentityValue(value) {
			return nil, fmt.Errorf("invalid identity config value: %s", key)
		}
		values[key] = value
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing identity config data")
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.ModTime() != after.ModTime() {
		return nil, fmt.Errorf("identity config changed while reading")
	}
	return values, nil
}
func resolveIdentity(root string) (repository.Identity, error) {
	global, err := globalConfigPath()
	if err != nil {
		return repository.Identity{}, err
	}
	values, err := readIdentityConfig(global)
	if err != nil {
		return repository.Identity{}, err
	}
	local, err := readIdentityConfig(filepath.Join(root, ".pebble", "config.json"))
	if err != nil {
		return repository.Identity{}, err
	}
	for key, value := range local {
		values[key] = value
	}
	get := func(role, field, legacy, env string) string {
		value := values["user."+field]
		if specific, ok := values[role+"."+field]; ok {
			value = specific
		}
		if override, ok := os.LookupEnv(legacy); ok {
			value = override
		}
		if override, ok := os.LookupEnv(env); ok {
			value = override
		}
		return strings.TrimSpace(value)
	}
	identity := repository.Identity{
		Author:         get("author", "name", "PEBBLE_AUTHOR", "PEBBLE_AUTHOR_NAME"),
		Email:          get("author", "email", "PEBBLE_EMAIL", "PEBBLE_AUTHOR_EMAIL"),
		Committer:      get("committer", "name", "PEBBLE_AUTHOR", "PEBBLE_COMMITTER_NAME"),
		CommitterEmail: get("committer", "email", "PEBBLE_EMAIL", "PEBBLE_COMMITTER_EMAIL"),
	}
	for _, value := range []string{identity.Author, identity.Email, identity.Committer, identity.CommitterEmail} {
		if !validIdentityValue(value) {
			return repository.Identity{}, fmt.Errorf("configure user.name/user.email or set PEBBLE_AUTHOR/PEBBLE_EMAIL; explicit empty or invalid identity is not a fallback")
		}
	}
	return identity, nil
}
func handleConfig(params []string, root string, w io.Writer) (err error) {
	global := len(params) > 0 && params[0] == "--global"
	if global || len(params) > 0 && params[0] == "--local" {
		params = params[1:]
	}
	if len(params) < 1 || len(params) > 2 || !identityKey(params[0]) {
		return fmt.Errorf("usage: pebble config [--global|--local] <user|author|committer>.<name|email> [value]")
	}
	var path string
	if global {
		path, err = globalConfigPath()
		if err != nil {
			return err
		}
	} else {
		if root == "" {
			return fmt.Errorf("local config requires a repository")
		}
		path = filepath.Join(root, ".pebble", "config.json")
	}
	values, err := readIdentityConfig(path)
	if err != nil {
		return err
	}
	if len(params) == 1 {
		value, ok := values[params[0]]
		if !ok {
			return fmt.Errorf("configuration key is not set in this scope: %s", params[0])
		}
		_, err = fmt.Fprintln(w, value)
		return err
	}
	if !validIdentityValue(params[1]) {
		return fmt.Errorf("invalid identity value")
	}
	values[params[0]] = params[1]
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".pebble-config-")
	if err != nil {
		return err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
