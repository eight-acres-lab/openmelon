package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/eight-acres-lab/openmelon/internal/registry"
)

// stringSlice is a flag.Value that collects repeated string flags.
//
// Used by --tag (so the user can write `--tag a --tag b` instead of
// inventing a comma-separated DSL).
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

// parseInterspersed parses args into fs while tolerating positional args
// before, between, or after flags. Stdlib flag.Parse stops at the first
// non-flag token; this wrapper hoists positionals to the end so flags
// after them are still parsed.
//
// The wrapper consults fs to distinguish bool flags (no following value)
// from valued flags. "--" is honored as an end-of-flags marker.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var positionals, hoisted []string
	end := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if end {
			positionals = append(positionals, a)
			continue
		}
		if a == "--" {
			end = true
			continue
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		hoisted = append(hoisted, a)
		// `--flag=value` carries its own value.
		if strings.Contains(a, "=") {
			continue
		}
		// Bool flags don't consume the next token. Look up the
		// underlying flag to decide.
		name := strings.TrimLeft(a, "-")
		f := fs.Lookup(name)
		if f != nil {
			if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue
			}
		}
		// Not a bool (or unknown — let flag.Parse error on it). Pull
		// the next token along as the flag's value.
		if i+1 < len(args) {
			hoisted = append(hoisted, args[i+1])
			i++
		}
	}
	combined := append(hoisted, positionals...)
	return fs.Parse(combined)
}

// runCharacter dispatches `openmelon character <add|list|show|rm>`.
func runCharacter(args []string) error {
	return runRegistryCmd(registry.KindCharacter, args)
}

// runReference dispatches `openmelon reference <add|list|show|rm>`.
func runReference(args []string) error {
	return runRegistryCmd(registry.KindReference, args)
}

// runMaterial dispatches `openmelon material <add|list>` (no rm/show
// since materials are hash-addressed and largely opaque).
func runMaterial(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: openmelon material <add|list> ...")
		os.Exit(2)
	}
	switch args[0] {
	case "add":
		return runMaterialAdd(args[1:])
	case "list":
		return runRegistryList(registry.KindMaterial, args[1:])
	default:
		return fmt.Errorf("unknown material subcommand: %q", args[0])
	}
}

func runRegistryCmd(kind registry.Kind, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: openmelon %s <add|list|show|rm> ...\n", kind)
		os.Exit(2)
	}
	switch args[0] {
	case "add":
		return runRegistryAdd(kind, args[1:])
	case "list":
		return runRegistryList(kind, args[1:])
	case "show":
		return runRegistryShow(kind, args[1:])
	case "rm", "remove":
		return runRegistryRm(kind, args[1:])
	default:
		return fmt.Errorf("unknown %s subcommand: %q", kind, args[0])
	}
}

func runRegistryAdd(kind registry.Kind, args []string) error {
	imageFlagName := "portrait"
	imageDestName := "portrait"
	if kind == registry.KindReference {
		imageFlagName = "image"
		imageDestName = "image"
	}

	fs := flag.NewFlagSet(string(kind)+" add", flag.ContinueOnError)
	name := fs.String("name", "", "Human-readable name (default: slug)")
	description := fs.String("description", "", "One- or two-sentence description; saved into .search")
	image := fs.String(imageFlagName, "", "Path to a "+imageFlagName+" image to copy into the item dir")
	update := fs.Bool("update", false, "Allow updating an existing item (default: error if it exists)")
	var tags stringSlice
	fs.Var(&tags, "tag", "Add a tag (repeatable). Tags must be kebab-case.")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: openmelon %s add <slug> [--name ...] [--description ...] [--%s path] [--tag t]...", kind, imageFlagName)
	}
	slug := fs.Arg(0)

	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	item, err := registry.Add(wd, registry.AddOptions{
		Kind:        kind,
		Slug:        slug,
		Name:        *name,
		Description: *description,
		Tags:        tags,
		ImagePath:   *image,
		ImageName:   imageDestName,
		AllowExists: *update,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Added %s %s\n", kind, item.Slug)
	if len(item.Images) > 0 {
		fmt.Printf("  images: %s\n", strings.Join(item.Images, ", "))
	}
	return nil
}

func runRegistryList(kind registry.Kind, args []string) error {
	fs := flag.NewFlagSet(string(kind)+" list", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	items, err := registry.List(wd, kind)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Printf("No %ss in this project.\n", kind)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tNAME\tIMAGES\tTAGS\tDESCRIPTION")
	for _, it := range items {
		desc := it.Description
		if len(desc) > 72 {
			desc = desc[:72] + "…"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			it.Slug, it.Name, len(it.Images), strings.Join(it.Tags, ","), desc)
	}
	return tw.Flush()
}

func runRegistryShow(kind registry.Kind, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: openmelon %s show <slug>", kind)
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	item, err := registry.Get(wd, kind, args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Kind:        %s\n", item.Kind)
	fmt.Printf("Slug:        %s\n", item.Slug)
	fmt.Printf("Name:        %s\n", item.Name)
	if item.Description != "" {
		fmt.Printf("Description: %s\n", item.Description)
	}
	if len(item.Tags) > 0 {
		fmt.Printf("Tags:        %s\n", strings.Join(item.Tags, ", "))
	}
	if len(item.Images) > 0 {
		fmt.Println("Images:")
		for _, img := range item.Images {
			fmt.Printf("  %s\n", img)
		}
	}
	if len(item.Extra) > 0 {
		fmt.Println("Metadata:")
		for k, v := range item.Extra {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
	return nil
}

func runRegistryRm(kind registry.Kind, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: openmelon %s rm <slug>", kind)
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	if err := registry.Remove(wd, kind, args[0]); err != nil {
		return err
	}
	fmt.Printf("Removed %s %s\n", kind, args[0])
	return nil
}

func runMaterialAdd(args []string) error {
	fs := flag.NewFlagSet("material add", flag.ContinueOnError)
	var tags stringSlice
	fs.Var(&tags, "tag", "Add a tag (repeatable)")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: openmelon material add <path> [--tag t]...")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	item, err := registry.AddMaterial(wd, fs.Arg(0), tags)
	if err != nil {
		return err
	}
	fmt.Printf("Added material %s\n", item.Slug)
	return nil
}
