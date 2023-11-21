package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/WangYihang/rug/pkg/version"
	"github.com/jessevdk/go-flags"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type Options struct {
	Count   int  `short:"c" long:"count" description:"Number of usernames to generate" default:"1"`
	Version bool `short:"v" long:"version" description:"Show version"`
}

var (
	opts  = &Options{}
	nouns = []string{}
	verbs = []string{}
)

func randomNoun() string {
	return nouns[rand.Intn(len(nouns))]
}

func randomVerb() string {
	return verbs[rand.Intn(len(verbs))]
}

func randomNumber() int {
	return rand.Intn(0x100)
}

func load(path string) chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		fd, err := os.Open(path)
		if err != nil {
			slog.Error("error occured while opening file", slog.String("path", path), slog.String("error", err.Error()))
			return
		}
		defer fd.Close()
		scanner := bufio.NewScanner(fd)
		for scanner.Scan() {
			for _, item := range strings.Split(strings.Trim(scanner.Text(), "_-'"), " ") {
				if len(item) < 8 {
					out <- item
				}
			}
		}
	}()
	return out
}

func randInt(min, max int) int {
	return min + rand.Intn(max-min)
}

func generate(n int) chan string {
	out := make(chan string)
	go func() {
		defer close(out)
		generator := []func() string{
			randomNoun,
			randomVerb,
		}
		for i := 0; i < n; i++ {
			numItems := randInt(2, 3)
			items := []string{}
			capitalizer := cases.Title(language.English, cases.Compact)
			for j := 0; j < numItems; j++ {
				item := capitalizer.String(
					generator[rand.Intn(len(generator))](),
				)
				items = append(items, item)
			}
			rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
			items = append(items, fmt.Sprintf("%d", randomNumber()))
			out <- strings.Join(items, "")
		}
	}()
	return out
}

func download(url, path string) {
	slog.Info("downloading file", slog.String("url", url), slog.String("path", path))

	// Create folder
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		slog.Error("error occured while creating folder", slog.String("path", path), slog.String("error", err.Error()))
		return
	}

	// Send http request
	response, err := http.Get(url)
	if err != nil {
		slog.Error("error occured while downloading file", slog.String("url", url), slog.String("error", err.Error()))
		return
	}
	defer response.Body.Close()

	// Create file
	fd, err := os.Create(path)
	if err != nil {
		slog.Error("error occured while creating file", slog.String("path", path), slog.String("error", err.Error()))
		return
	}
	defer fd.Close()

	// Write to file
	bar := progressbar.DefaultBytes(
		response.ContentLength,
		"downloading",
	)
	io.Copy(io.MultiWriter(fd, bar), response.Body)

	// Log
	slog.Info("file downloaded", slog.String("url", url), slog.String("path", path))
}

func setup(dictFolder string) {
	// Download dictionary
	url := "https://wordnetcode.princeton.edu/wn3.1.dict.tar.gz"
	dictFilepath := filepath.Join(dictFolder, filepath.Base(url))
	if _, err := os.Stat(dictFilepath); !os.IsNotExist(err) {
		return
	}
	download(url, dictFilepath)
	// Extract dictionary
	fd, err := os.Open(dictFilepath)
	if err != nil {
		slog.Error("error occurred while opening dictionary file", slog.String("path", dictFilepath), slog.String("error", err.Error()))
		return
	}
	defer fd.Close()
	err = unar(fd, dictFolder)
	if err != nil {
		slog.Error("error occurred while extracting dictionary file", slog.String("path", dictFilepath), slog.String("error", err.Error()))
		return
	}
	slog.Info("dictionary extracted", slog.String("path", dictFilepath))
}

func unar(gzipStream io.Reader, folder string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	err = os.Chdir(folder)
	if err != nil {
		return err
	}
	uncompressedStream, err := gzip.NewReader(gzipStream)
	if err != nil {
		return err
	}
	tarReader := tar.NewReader(uncompressedStream)
	var header *tar.Header
	for header, err = tarReader.Next(); err == nil; header, err = tarReader.Next() {
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(header.Name, 0755); err != nil {
				return fmt.Errorf("unar: MkdirAll() failed: %w", err)
			}
		case tar.TypeReg:
			outFile, err := os.Create(header.Name)
			if err != nil {
				return fmt.Errorf("unar: Create() failed: %w", err)
			}

			if _, err := io.Copy(outFile, tarReader); err != nil {
				// outFile.Close error omitted as Copy error is more interesting at this point
				outFile.Close()
				return fmt.Errorf("unar: Copy() failed: %w", err)
			}
			if err := outFile.Close(); err != nil {
				return fmt.Errorf("unar: Close() failed: %w", err)
			}
		default:
			return fmt.Errorf("unar: uknown type: %b in %s", header.Typeflag, header.Name)
		}
	}
	if err != io.EOF {
		return fmt.Errorf("unar: Next() failed: %w", err)
	}
	err = os.Chdir(cwd)
	if err != nil {
		return err
	}
	return nil
}

func init() {
	// Parse command line arguments
	_, err := flags.ParseArgs(opts, os.Args)
	if err != nil {
		os.Exit(1)
	}

	// Display version
	if opts.Version {
		fmt.Fprintln(os.Stderr, version.Tag)
		os.Exit(0)
	}

	// Retrieve user home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Error("error occured while getting user home directory", slog.String("error", err.Error()))
		return
	}

	// Setup dictionary
	dictFolder := filepath.Join(homeDir, ".rug")
	setup(dictFolder)

	// Load noun dictionary
	slog.Debug("loading noun dictionary", slog.String("path", dictFolder))
	nounsFilepath := filepath.Join(dictFolder, "dict", "noun.exc")
	for noun := range load(nounsFilepath) {
		nouns = append(nouns, noun)
	}
	slog.Debug("noun dictionary loaded", slog.Int("count", len(nouns)))

	// Load verb dictionary
	slog.Debug("loading verb dictionary", slog.String("path", dictFolder))
	verbsFilepath := filepath.Join(dictFolder, "dict", "verb.exc")
	for verb := range load(verbsFilepath) {
		verbs = append(verbs, verb)
	}
	slog.Debug("verb dictionary loaded", slog.Int("count", len(verbs)))
}

func main() {
	for v := range generate(opts.Count) {
		println(v)
	}
}
