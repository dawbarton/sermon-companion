package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The settings are edited by hand from what the documents say, so the settings
// reference must name every field config.json can hold, and every example in
// the documents must be accepted by the strict reader the application uses.
// Without these checks a renamed or added setting leaves the documents wrong
// with nothing to say so.

const settingsReference = "../../docs/CONFIGURATION.md"

func TestSettingsReferenceNamesEverySetting(t *testing.T) {
	data, err := os.ReadFile(settingsReference)
	if err != nil {
		t.Fatal(err)
	}
	reference := string(data)
	for _, name := range settingNames(reflect.TypeOf(Config{}), "") {
		if !strings.Contains(reference, "`"+name+"`") {
			t.Errorf("%s does not describe the setting `%s`", settingsReference, name)
		}
	}
}

// settingNames lists the JSON names of a configuration type's fields, with a
// nested object's fields written as parent.child. The fields of a list's
// entries, such as a preset's kind and label, are listed by their own names.
func settingNames(typ reflect.Type, prefix string) []string {
	var names []string
	for index := range typ.NumField() {
		field := typ.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		names = append(names, prefix+name)
		inner := field.Type
		for inner.Kind() == reflect.Pointer {
			inner = inner.Elem()
		}
		switch {
		case inner.Kind() == reflect.Struct:
			names = append(names, settingNames(inner, prefix+name+".")...)
		case inner.Kind() == reflect.Slice && inner.Elem().Kind() == reflect.Struct:
			names = append(names, settingNames(inner.Elem(), "")...)
		}
	}
	return names
}

var jsonExample = regexp.MustCompile("(?ms)^[ \t]*```json\n(.*?)^[ \t]*```")

func TestDocumentedExamplesAreAccepted(t *testing.T) {
	for _, document := range []string{"../../README.md", settingsReference} {
		data, err := os.ReadFile(document)
		if err != nil {
			t.Fatal(err)
		}
		examples := jsonExample.FindAllStringSubmatch(string(data), -1)
		if len(examples) == 0 {
			t.Errorf("%s has no JSON examples to check", document)
		}
		for _, example := range examples {
			// An example is a fragment of config.json: one or more entries,
			// perhaps with the comma that separates it from the next.
			fragment := strings.TrimSuffix(strings.TrimSpace(example[1]), ",")
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte("{\n"+fragment+"\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreateConfig(path); err != nil {
				t.Errorf("%s: an example is not accepted: %v\n%s", document, err, fragment)
			}
		}
	}
}
