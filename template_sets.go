package pongo2

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"sync"
)

type TemplateLoader interface {
	Abs(base, name string) string
	Get(path string) (io.Reader, error)
}

// A template set allows you to create your own group of templates with their own global context (which is shared
// among all members of the set), their own configuration (like a specific base directory) and their own sandbox.
// It's useful for a separation of different kind of templates (e. g. web templates vs. mail templates).
type TemplateSet struct {
	name   string
	loader TemplateLoader

	// Globals will be provided to all templates created within this template set
	Globals Context

	// If debug is true (default false), ExecutionContext.Logf() will work and output to STDOUT. Furthermore,
	// FromCache() won't cache the templates. Make sure to synchronize the access to it in case you're changing this
	// variable during program execution (and template compilation/execution).
	Debug bool

	// Sandbox features
	// - Limit access to directories (using SandboxDirectories)
	// - Disallow access to specific tags and/or filters (using BanTag() and BanFilter())
	//
	// You can limit file accesses (for all tags/filters which are using pongo2's file resolver technique)
	// to these sandbox directories. All default pongo2 filters/tags are respecting these restrictions.
	// For example, if you only have your base directory in the list, a {% ssi "/etc/passwd" %} will not work.
	// No items in SandboxDirectories means no restrictions at all.
	//
	// For efficiency reasons you can ban tags/filters only *before* you have added your first
	// template to the set (restrictions are statically checked). After you added one, it's not possible anymore
	// (for your personal security).
	firstTemplateCreated bool
	bannedTags           map[string]bool
	bannedFilters        map[string]bool

	// Template cache (for FromCache())
	templateCache      map[string]*Template
	templateCacheMutex sync.Mutex
}

// Create your own template sets to separate different kind of templates (e. g. web from mail templates) with
// different globals or other configurations (like base directories).
func NewSet(name string, loader TemplateLoader) *TemplateSet {
	return &TemplateSet{
		name:          name,
		loader:        loader,
		Globals:       make(Context),
		bannedTags:    make(map[string]bool),
		bannedFilters: make(map[string]bool),
		templateCache: make(map[string]*Template),
	}
}

func (set *TemplateSet) resolveFilename(tpl *Template, path string) string {
	name := ""
	if tpl != nil && tpl.isTplString {
		return path
	}
	if tpl != nil {
		name = tpl.name
	}
	return set.loader.Abs(name, path)
}

// BanTag bans a specific tag for this template set. See more in the documentation for TemplateSet.
func (set *TemplateSet) BanTag(name string) error {
	_, has := tags[name]
	if !has {
		return fmt.Errorf("Tag '%s' not found.", name)
	}
	if set.firstTemplateCreated {
		return errors.New("You cannot ban any tags after you've added your first template to your template set.")
	}
	_, has = set.bannedTags[name]
	if has {
		return fmt.Errorf("Tag '%s' is already banned.", name)
	}
	set.bannedTags[name] = true

	return nil
}

// BanFilter bans a specific filter for this template set. See more in the documentation for TemplateSet.
func (set *TemplateSet) BanFilter(name string) error {
	_, has := filters[name]
	if !has {
		return fmt.Errorf("Filter '%s' not found.", name)
	}
	if set.firstTemplateCreated {
		return errors.New("You cannot ban any filters after you've added your first template to your template set.")
	}
	_, has = set.bannedFilters[name]
	if has {
		return fmt.Errorf("Filter '%s' is already banned.", name)
	}
	set.bannedFilters[name] = true

	return nil
}

// FromCache() is a convenient method to cache templates. It is thread-safe
// and will only compile the template associated with a filename once.
// If TemplateSet.Debug is true (for example during development phase),
// FromCache() will not cache the template and instead recompile it on any
// call (to make changes to a template live instantaneously).
// Like FromFile(), FromCache() takes a relative path to a set base directory.
// Sandbox restrictions apply (if given).
func (set *TemplateSet) FromCache(filename string) (*Template, error) {
	if set.Debug {
		// Recompile on any request
		return set.FromFile(filename)
	}
	// Cache the template
	cleanedFilename := set.resolveFilename(nil, filename)

	set.templateCacheMutex.Lock()
	defer set.templateCacheMutex.Unlock()

	tpl, has := set.templateCache[cleanedFilename]

	// Cache miss
	if !has {
		tpl, err := set.FromFile(cleanedFilename)
		if err != nil {
			return nil, err
		}
		set.templateCache[cleanedFilename] = tpl
		return tpl, nil
	}

	// Cache hit
	return tpl, nil
}

// FromString loads a template from string and returns a Template instance.
func (set *TemplateSet) FromString(tpl string) (*Template, error) {
	set.firstTemplateCreated = true

	return newTemplateString(set, []byte(tpl))
}

// FromFile loads a template from a filename and returns a Template instance.
// If a base directory is set, the filename must be either relative to it
// or be an absolute path. Sandbox restrictions (SandboxDirectories) apply
// if given.
func (set *TemplateSet) FromFile(filename string) (*Template, error) {
	set.firstTemplateCreated = true

	fd, err := set.loader.Get(set.resolveFilename(nil, filename))
	if err != nil {
		return nil, &Error{
			Filename: filename,
			Sender:   "fromfile",
			ErrorMsg: err.Error(),
		}
	}
	buf, err := ioutil.ReadAll(fd)
	if err != nil {
		return nil, &Error{
			Filename: filename,
			Sender:   "fromfile",
			ErrorMsg: err.Error(),
		}
	}

	return newTemplate(set, filename, false, buf)
}

// RenderTemplateString is a shortcut and renders a template string directly.
// Panics when providing a malformed template or an error occurs during execution.
func (set *TemplateSet) RenderTemplateString(s string, ctx Context) string {
	set.firstTemplateCreated = true

	tpl := Must(set.FromString(s))
	result, err := tpl.Execute(ctx)
	if err != nil {
		panic(err)
	}
	return result
}

// RenderTemplateFile is a shortcut and renders a template file directly.
// Panics when providing a malformed template or an error occurs during execution.
func (set *TemplateSet) RenderTemplateFile(fn string, ctx Context) string {
	set.firstTemplateCreated = true

	tpl := Must(set.FromFile(fn))
	result, err := tpl.Execute(ctx)
	if err != nil {
		panic(err)
	}
	return result
}

func (set *TemplateSet) logf(format string, args ...interface{}) {
	if set.Debug {
		logger.Printf(fmt.Sprintf("[template set: %s] %s", set.name, format), args...)
	}
}

// Logging function (internally used)
func logf(format string, items ...interface{}) {
	if debug {
		logger.Printf(format, items...)
	}
}

var (
	debug  bool // internal debugging
	logger = log.New(os.Stdout, "[pongo2] ", log.LstdFlags|log.Lshortfile)

	// Creating a default set
	DefaultLoader = MustNewLocalFileSystemLoader("")
	DefaultSet    = NewSet("default", DefaultLoader)

	// Methods on the default set
	FromString           = DefaultSet.FromString
	FromFile             = DefaultSet.FromFile
	FromCache            = DefaultSet.FromCache
	RenderTemplateString = DefaultSet.RenderTemplateString
	RenderTemplateFile   = DefaultSet.RenderTemplateFile

	// Globals for the default set
	Globals = DefaultSet.Globals
)
