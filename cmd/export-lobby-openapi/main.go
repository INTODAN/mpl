// Command export-lobby-openapi writes the player-facing lobby OpenAPI
// document without constructing any production infrastructure.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"islandBattles/lobby/httpapi"
)

const (
	apiTitle   = "Generic Lobby API"
	apiVersion = "1.0.0"

	eventSchemaRefPrefix = "#/components/schemas/"
	eventDefsRefPrefix   = "#/$defs/"
)

type artifact struct {
	openapi []byte
	events  []byte
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("export-lobby-openapi", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write OpenAPI JSON to PATH instead of stdout")
	eventsOutput := flags.String("events-output", "", "write the standalone invalidation JSON Schema to PATH")
	check := flags.Bool("check", false, "check explicit output files without writing them")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *check && *output == "" && *eventsOutput == "" {
		return errors.New("--check requires --output and/or --events-output")
	}
	if *output != "" && *eventsOutput != "" && filepath.Clean(*output) == filepath.Clean(*eventsOutput) {
		return errors.New("--output and --events-output must name different files")
	}

	result, err := buildArtifacts()
	if err != nil {
		return fmt.Errorf("export lobby OpenAPI: %w", err)
	}
	if *check {
		return checkArtifacts(*output, result.openapi, *eventsOutput, result.events)
	}

	// Prepare all destinations before renaming any of them. This makes a bad
	// second destination unable to replace a valid first artifact.
	files := make([]pendingFile, 0, 2)
	if *output != "" {
		files = append(files, pendingFile{path: *output, contents: result.openapi})
	}
	if *eventsOutput != "" {
		files = append(files, pendingFile{path: *eventsOutput, contents: result.events})
	}
	if err := writeArtifacts(files); err != nil {
		return err
	}
	if *output == "" {
		if _, err := stdout.Write(result.openapi); err != nil {
			return fmt.Errorf("write OpenAPI JSON to stdout: %w", err)
		}
	}
	return nil
}

// buildArtifacts constructs a fresh API each time. No handler is called while
// registering routes, so nil ports are sufficient and no deployment settings
// are needed for this offline command.
func buildArtifacts() (artifact, error) {
	router := chi.NewRouter()
	config := huma.DefaultConfig(apiTitle, apiVersion)
	// The exporter is not a server. Avoid adding Huma's documentation routes to
	// the player contract while retaining its normal schema generation hooks.
	config.OpenAPIPath = ""
	config.DocsPath = ""
	config.SchemasPath = ""
	api := humachi.New(router, config)
	httpapi.Register(api, nil, nil, nil)

	openapi, err := prettyJSONFrom(api.OpenAPI())
	if err != nil {
		return artifact{}, fmt.Errorf("marshal OpenAPI: %w", err)
	}
	events, err := standaloneEventSchema(api.OpenAPI())
	if err != nil {
		return artifact{}, fmt.Errorf("marshal event schema: %w", err)
	}
	return artifact{openapi: openapi, events: events}, nil
}

func prettyJSONFrom(value any) ([]byte, error) {
	compact, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return indentJSON(compact)
}

func indentJSON(compact []byte) ([]byte, error) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

func standaloneEventSchema(spec *huma.OpenAPI) ([]byte, error) {
	if spec == nil || spec.Components == nil || spec.Components.Schemas == nil {
		return nil, errors.New("registered schema components are missing")
	}
	rootSchema, ok := spec.Components.Schemas.Map()["LobbyInvalidation"]
	if !ok || rootSchema == nil {
		return nil, errors.New("LobbyInvalidation schema is not registered")
	}
	rootBytes, err := json.Marshal(rootSchema)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(rootBytes, &root); err != nil {
		return nil, err
	}
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["title"] = "LobbyInvalidation"

	registry := spec.Components.Schemas.Map()
	needed := map[string]bool{}
	if err := rewriteAndCollectRefs(root, needed); err != nil {
		return nil, err
	}
	defs := map[string]any{}
	for len(needed) != 0 {
		var name string
		for candidate := range needed {
			name = candidate
			break
		}
		delete(needed, name)
		if _, exists := defs[name]; exists {
			continue
		}
		schema, exists := registry[name]
		if !exists || schema == nil {
			return nil, fmt.Errorf("schema reference %q is not registered", name)
		}
		data, err := json.Marshal(schema)
		if err != nil {
			return nil, fmt.Errorf("marshal schema %q: %w", name, err)
		}
		var definition map[string]any
		if err := json.Unmarshal(data, &definition); err != nil {
			return nil, err
		}
		if err := rewriteAndCollectRefs(definition, needed); err != nil {
			return nil, err
		}
		defs[name] = definition
	}
	if len(defs) > 0 {
		root["$defs"] = defs
	}
	return prettyJSONFrom(root)
}

// rewriteAndCollectRefs converts OpenAPI component references into local JSON
// Schema definitions. It also rejects references that cannot be self-contained
// in events.json.
func rewriteAndCollectRefs(value any, needed map[string]bool) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "$ref" {
				ref, ok := child.(string)
				if !ok {
					return errors.New("event schema contains a non-string $ref")
				}
				switch {
				case strings.HasPrefix(ref, eventSchemaRefPrefix):
					name := strings.TrimPrefix(ref, eventSchemaRefPrefix)
					if name == "" {
						return errors.New("event schema contains an empty component reference")
					}
					typed[key] = eventDefsRefPrefix + name
					needed[name] = true
				case strings.HasPrefix(ref, eventDefsRefPrefix):
					name := strings.TrimPrefix(ref, eventDefsRefPrefix)
					if name == "" {
						return errors.New("event schema contains an empty local reference")
					}
					needed[name] = true
				default:
					return fmt.Errorf("event schema reference %q cannot be resolved locally", ref)
				}
				continue
			}
			if err := rewriteAndCollectRefs(child, needed); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := rewriteAndCollectRefs(child, needed); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkArtifacts(output string, openapi []byte, eventsOutput string, events []byte) error {
	for _, expected := range []struct {
		path string
		data []byte
		name string
	}{
		{output, openapi, "OpenAPI"},
		{eventsOutput, events, "event schema"},
	} {
		if expected.path == "" {
			continue
		}
		actual, err := os.ReadFile(expected.path)
		if err != nil {
			return fmt.Errorf("check %s %q: %w", expected.name, expected.path, err)
		}
		if !bytes.Equal(actual, expected.data) {
			return fmt.Errorf("check %s %q: artifact differs", expected.name, expected.path)
		}
	}
	return nil
}

type pendingFile struct {
	path     string
	contents []byte
}

func writeArtifacts(files []pendingFile) error {
	temps := make([]string, 0, len(files))
	cleanup := func() {
		for _, name := range temps {
			_ = os.Remove(name)
		}
	}
	for _, file := range files {
		if file.path == "" {
			cleanup()
			return errors.New("artifact path cannot be empty")
		}
		directory := filepath.Dir(file.path)
		base := filepath.Base(file.path)
		temporary, err := os.CreateTemp(directory, "."+base+".tmp-")
		if err != nil {
			cleanup()
			return fmt.Errorf("prepare artifact %q: %w", file.path, err)
		}
		temporaryName := temporary.Name()
		temps = append(temps, temporaryName)
		_, err = temporary.Write(file.contents)
		if err == nil {
			err = temporary.Close()
			if err == nil {
				err = os.Chmod(temporaryName, 0o644)
			}
		}
		if err != nil {
			_ = temporary.Close()
			cleanup()
			return fmt.Errorf("prepare artifact %q: %w", file.path, err)
		}
	}
	for index, file := range files {
		if err := os.Rename(temps[index], file.path); err != nil {
			cleanup()
			return fmt.Errorf("replace artifact %q: %w", file.path, err)
		}
	}
	return nil
}
