package main

import (
	"fmt"
	"maps"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	ssDest       = "org.freedesktop.secrets"
	ssPath       = "/org/freedesktop/secrets"
	ssCollection = "/org/freedesktop/secrets/aliases/default"
)

type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// exactAttrs reports whether got is want, optionally plus the xdg:schema
// attribute that secret-tool store adds.
func exactAttrs(got, want map[string]string) bool {
	got = maps.Clone(got)
	delete(got, "xdg:schema")
	return maps.Equal(got, want)
}

// readPAT reads the GitLab token for host from the Secret Service default
// collection (attributes service=gitlab host=<host>).
func readPAT(host string) (string, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return "", fmt.Errorf("connecting to session bus: %w", err)
	}

	svc := conn.Object(ssDest, ssPath)
	var out dbus.Variant
	var session dbus.ObjectPath
	if err := svc.Call("org.freedesktop.Secret.Service.OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session); err != nil {
		return "", fmt.Errorf("opening secret service session: %w", err)
	}

	want := map[string]string{"service": "gitlab", "host": host}
	var found []dbus.ObjectPath
	if err := conn.Object(ssDest, ssCollection).Call("org.freedesktop.Secret.Collection.SearchItems", 0, want).Store(&found); err != nil {
		return "", fmt.Errorf("searching secret service: %w", err)
	}

	var items []dbus.ObjectPath
	for _, p := range found {
		v, err := conn.Object(ssDest, p).GetProperty("org.freedesktop.Secret.Item.Attributes")
		if err != nil {
			return "", fmt.Errorf("reading secret attributes: %w", err)
		}
		if attrs, ok := v.Value().(map[string]string); ok && exactAttrs(attrs, want) {
			items = append(items, p)
		}
	}
	if len(items) == 0 {
		return "", fmt.Errorf("secret service item service=gitlab host=%s is missing", host)
	}
	if len(items) > 1 {
		return "", fmt.Errorf("multiple secret service items match service=gitlab host=%s", host)
	}

	var done []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := svc.Call("org.freedesktop.Secret.Service.Unlock", 0, items).Store(&done, &prompt); err != nil {
		return "", fmt.Errorf("unlocking secret: %w", err)
	}
	if prompt != "/" {
		if done, err = promptUnlock(conn, prompt); err != nil {
			return "", err
		}
	}

	var secrets map[dbus.ObjectPath]ssSecret
	if err := svc.Call("org.freedesktop.Secret.Service.GetSecrets", 0, done, session).Store(&secrets); err != nil {
		return "", fmt.Errorf("getting secret: %w", err)
	}
	s, ok := secrets[items[0]]
	if !ok {
		return "", fmt.Errorf("secret service item service=gitlab host=%s is locked", host)
	}

	token := strings.TrimSpace(string(s.Value))
	if token == "" {
		return "", fmt.Errorf("secret service item service=gitlab host=%s is empty", host)
	}
	return token, nil
}

func promptUnlock(conn *dbus.Conn, prompt dbus.ObjectPath) ([]dbus.ObjectPath, error) {
	ch := make(chan *dbus.Signal, 8)
	conn.Signal(ch)
	defer conn.RemoveSignal(ch)
	if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(prompt), dbus.WithMatchInterface("org.freedesktop.Secret.Prompt"), dbus.WithMatchMember("Completed")); err != nil {
		return nil, err
	}
	if err := conn.Object(ssDest, prompt).Call("org.freedesktop.Secret.Prompt.Prompt", 0, "").Err; err != nil {
		return nil, fmt.Errorf("prompting for unlock: %w", err)
	}
	for sig := range ch {
		if sig.Path != prompt || len(sig.Body) != 2 {
			continue
		}
		if dismissed, _ := sig.Body[0].(bool); dismissed {
			return nil, fmt.Errorf("unlock dismissed")
		}
		res, _ := sig.Body[1].(dbus.Variant)
		paths, _ := res.Value().([]dbus.ObjectPath)
		return paths, nil
	}
	return nil, fmt.Errorf("unlock prompt aborted")
}
