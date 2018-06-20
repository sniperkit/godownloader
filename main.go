package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"text/template"
	"time"

	"github.com/goreleaser/goreleaser/config"
	"github.com/goreleaser/goreleaser/context"
	"github.com/goreleaser/goreleaser/pipeline/defaults"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	"github.com/client9/codegen/shell"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	version = "dev"
	commit  = "none"
	datestr = "unknown"
)

func init() {
	log.SetHandler(cli.Default)
}

// given a template, and a config, generate shell script
func makeShell(tplsrc string, cfg *config.Project) ([]byte, error) {

	// if we want to add a timestamp in the templates this
	//  function will generate it
	funcMap := template.FuncMap{
		"timestamp": func() string {
			return time.Now().UTC().Format(time.RFC3339)
		},
	}

	out := bytes.Buffer{}
	t, err := template.New("shell").Funcs(funcMap).Parse(tplsrc)
	if err != nil {
		return nil, err
	}
	err = t.Execute(&out, cfg)
	return out.Bytes(), err
}

// converts the given name template to it's equivalent in shell
// except for the default goreleaser templates, templates with
// conditionals will return an error
//
// {{ .Binary }} --->  [prefix]${BINARY}, etc.
//
func makeName(prefix, target string) (string, error) {
	// armv6 is the default in the shell script
	// so do not need special template condition for ARM
	armversion := "{{ .Arch }}{{ if .Arm }}v{{ .Arm }}{{ end }}"
	target = strings.Replace(target, armversion, "{{ .Arch }}", -1)

	// hack for https://github.com/sniperkit/godownloader/issues/70
	armversion = "{{ .Arch }}{{ if .Arm }}{{ .Arm }}{{ end }}"
	target = strings.Replace(target, armversion, "{{ .Arch }}", -1)

	// otherwise if it contains a conditional, we can't (easily)
	// translate that to bash.  Ask for bug report.
	if strings.Contains(target, "{{ if") || strings.Contains(target, "{{if") || strings.Contains(target, "{{ .Arm") || strings.Contains(target, "{{.Arm") {
		return "", fmt.Errorf("name_template %q contains unknown conditional or ARM format.  Please file bug at https://github.com/sniperkit/godownloader", target)
	}

	varmap := map[string]string{
		"Os":          "${OS}",
		"Arch":        "${ARCH}",
		"Version":     "${VERSION}",
		"Tag":         "${TAG}",
		"Binary":      "${BINARY}",
		"ProjectName": "${PROJECT_NAME}",
	}

	out := bytes.Buffer{}
	out.WriteString(prefix)
	t, err := template.New("name").Parse(target)
	if err != nil {
		return "", err
	}
	err = t.Execute(&out, varmap)
	return out.String(), err
}

// returns the owner/name repo from input
//
// see https://github.com/sniperkit/godownloader/issues/55
func normalizeRepo(repo string) string {
	// handle full or partial URLs
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimPrefix(repo, "http://github.com/")
	repo = strings.TrimPrefix(repo, "github.com/")

	// hande /name/repo or name/repo/ cases
	repo = strings.Trim(repo, "/")

	return repo
}

func loadURLs(path, configPath string) (*config.Project, error) {
	for _, file := range []string{configPath, "goreleaser.yml", ".goreleaser.yml", "goreleaser.yaml", ".goreleaser.yaml"} {
		if file == "" {
			continue
		}
		url := fmt.Sprintf("%s/%s", path, file)
		log.Infof("reading %s", url)
		project, err := loadURL(url)
		if err != nil {
			return nil, err
		}
		if project != nil {
			return project, nil
		}
	}
	return nil, fmt.Errorf("could not fetch a goreleaser configuration file")
}

func loadURL(file string) (*config.Project, error) {
	resp, err := http.Get(file)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		log.Errorf("reading %s returned %d %s\n", file, resp.StatusCode, http.StatusText(resp.StatusCode))
		return nil, nil
	}
	p, err := config.LoadReader(resp.Body)

	// to make errcheck happy
	errc := resp.Body.Close()
	if errc != nil {
		return nil, errc
	}
	return &p, err
}

func loadFile(file string) (*config.Project, error) {
	p, err := config.Load(file)
	return &p, err
}

// Load project configuration from a given repo name or filepath/url.
func Load(repo, configPath, file string) (project *config.Project, err error) {
	if repo == "" && file == "" {
		return nil, fmt.Errorf("repo or file not specified")
	}
	if file == "" {
		repo = normalizeRepo(repo)
		log.Infof("reading repo %q on github", repo)
		project, err = loadURLs(
			fmt.Sprintf("https://raw.githubusercontent.com/%s/master", repo),
			configPath,
		)
	} else {
		log.Infof("reading file %q", file)
		project, err = loadFile(file)
	}
	if err != nil {
		return nil, err
	}

	// if not specified add in GitHub owner/repo info
	if project.Release.GitHub.Owner == "" {
		if repo == "" {
			return nil, fmt.Errorf("owner/name repo not specified")
		}
		project.Release.GitHub.Owner = path.Dir(repo)
		project.Release.GitHub.Name = path.Base(repo)
	}

	ctx := context.New(*project)
	err = defaults.Pipe{}.Run(ctx)
	project = &ctx.Config

	// set default binary name
	if len(project.Builds) == 0 {
		project.Builds = []config.Build{
			{Binary: path.Base(repo)},
		}
	}
	if project.Builds[0].Binary == "" {
		project.Builds[0].Binary = path.Base(repo)
	}

	return project, err
}

func main() {
	var (
		repo    = kingpin.Flag("repo", "owner/name or URL of GitHub repository").Short('r').String()
		output  = kingpin.Flag("output", "output file, default stdout").Short('o').String()
		force   = kingpin.Flag("force", "force writing of output").Short('f').Bool()
		source  = kingpin.Flag("source", "source type [godownloader|raw|equinoxio]").Default("godownloader").String()
		exe     = kingpin.Flag("exe", "name of binary, used only in raw").String()
		nametpl = kingpin.Flag("nametpl", "name template, used only in raw").String()
		tree    = kingpin.Flag("tree", "use tree to generate multiple outputs").String()
		file    = kingpin.Arg("file", "??").String()
	)

	kingpin.CommandLine.Version(fmt.Sprintf("%v, commit %v, built at %v", version, commit, datestr))
	kingpin.CommandLine.VersionFlag.Short('v')
	kingpin.CommandLine.HelpFlag.Short('h')
	kingpin.Parse()

	if *tree != "" {
		err := treewalk(*tree, *file, *force)
		if err != nil {
			log.WithError(err).Error("treewalker failed")
			os.Exit(1)
		}
		return
	}

	// gross.. need config
	out, err := processSource(*source, *repo, "", *file, *exe, *nametpl)

	if err != nil {
		log.WithError(err).Error("failed")
		os.Exit(1)
	}

	// stdout case
	if *output == "" {
		if _, err = os.Stdout.Write(out); err != nil {
			log.WithError(err).Error("unable to write")
			os.Exit(1)
		}
		return
	}

	// only write out if forced to, OR if output is effectively different
	// than what the file has.
	if *force || shell.ShouldWriteFile(*output, out) {
		if err = ioutil.WriteFile(*output, out, 0666); err != nil {
			log.WithError(err).Errorf("unable to write to %s", *output)
			os.Exit(1)
		}
		return
	}

	// output is effectively the same as new content
	// (comments and most whitespace doesn't matter)
	// nothing to do
}
