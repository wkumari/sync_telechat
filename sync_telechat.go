// This program read the upcoming IESG telechats, downloads PDF versions
// of each new document, and places them in directories according to the date.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/glog"
	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
)

const (
	// I am using leading k for constants, in violation of GoLint
	// Where the JSON version of the IESG agenda lives.
	kJSONURL = "https://datatracker.ietf.org/iesg/agenda/agenda.json"

	// Where the PDF versions of drafts live.
	kDocURL = "https://tools.ietf.org/pdf/"
)

type options struct {
	basedir string
	baseurl string

	debug   bool
	verbose bool
}

var (
	opts = options{}
)

// Expand home directory. Only works on Unix type systems.
// No sure why Go doesn't include a helper for this (well, I am, I just don't
// agree :-) )
func expand(path string) (string, error) {
	if len(path) == 0 || !strings.HasPrefix(path, "~") {
		return path, nil
	}

	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(u.HomeDir, path[1:]), nil
}

// Fetches a single document, puts it in the directory specified by date.
// Notifies we are done by posting to done!
func fetchDoc(basedir string, date string, document string, done chan string) {

	filename := document + ".pdf"
	url := kDocURL + filename

	// If this fails because the file already exists, we are done!
	fullname := filepath.Join(basedir, date, filename)
	var _, err = os.Stat(fullname)
	if err == nil {
		done <- fmt.Sprintf("%v: %v already existed.", date, filename)
		return
	}
	output, err := os.Create(fullname)
	if err != nil {
		done <- fmt.Sprintf("Error creating %v: %v", fullname, err.Error())
		return
	}
	defer output.Close()

	response, err := http.Get(url)
	if err != nil {
		done <- fmt.Sprintf("Error while downloading %v-%v", url, err.Error())
		return
	}
	defer response.Body.Close()

	n, err := io.Copy(output, response.Body)
	if err != nil {
		done <- fmt.Sprintf("Error while downloading: %v - %v ", url, err.Error())
		return
	}
	done <- fmt.Sprintf("%v: Downloaded %s: %d bytes.", date, filename, n)
}

// Fetches documents in parallel.
//
// Takes a map of slices, {"date": [doc1, doc2]} and
// gets the documents.
func fetchDocs(basedir string, documents map[string][]string) []string {

	// Make directories if not already exist
	for date := range documents {
		err := os.Mkdir(filepath.Join(basedir, date), 0777)
		if err != nil && !os.IsExist(err) {
			glog.Fatal(fmt.Sprintf("Error making %v: %v", filepath.Join(basedir, date), err.Error()))
		}
	}

	// Channels
	doccount := 0
	var items []string
	channel := make(chan string)
	for date := range documents {
		for _, document := range documents[date] {
			go fetchDoc(basedir, date, document, channel)
			doccount++
		}
	}
	for len(items) < doccount {
		select {
		case downloaded := <-channel:
			items = append(items, downloaded)

		case <-time.After(time.Second * 10):
			items = append(items, "Timeout downloading a draft....")
		}
	}
	close(channel)
	return items
}

func fetchAgenda(url string) (map[string][]string, error) {
	result := make(map[string][]string)

	var agenda map[string]interface{}
	var sections map[string]interface{}

	resp, err := http.Get(url)
	if err != nil {
		log.Error("ERROR: " + err.Error())
		return result, fmt.Errorf("error fetching agenda: %v", err)
	}

	if resp.Body != nil {
		defer resp.Body.Close()
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Error(err)
		return result, fmt.Errorf("error reading agenda: %v", err)
	}

	err = json.Unmarshal(body, &agenda)
	if err != nil {
		log.Errorf("Error unmarshalling agenda: %v", err)
		return result, fmt.Errorf("error unmarshalling agenda: %v", err)
	}

	date := agenda["telechat-date"].(string)
	sections = agenda["sections"].(map[string]interface{})
	for section := range sections {
		content := sections[section].(map[string]interface{})
		if content["docs"] != nil {
			docs := content["docs"]
			if len(docs.([]interface{})) > 0 {
				for _, doc := range docs.([]interface{}) {
					doc := doc.(map[string]interface{})
					docname := doc["docname"].(string)
					rev := doc["rev"].(string)
					log.Debugf("Doc: %s (date: %s)", docname, date)
					result[date] = append(result[date], docname+"-"+rev)
				}
			}
		}
	}
	return result, nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "Syncs the IESG Telechat to local directories.\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func parseFlags() {
	flag.Usage = usage
	log.SetLevel(log.WarnLevel)

	// Flags:  long name, short name, default value, description
	flag.StringVar(&opts.basedir, "basedir", "",
		"Base directory to put files. Makes date based directories here.")
	flag.StringVar(&opts.baseurl, "agenda", kJSONURL,
		"Where the agenda lives")

	flag.BoolVarP(&opts.verbose, "verbose", "v", false, "be more verbose.")
	flag.BoolVarP(&opts.debug, "debug", "d", false, "print debug information.")

	flag.Parse()

	if opts.basedir == "" {
		log.Fatal("You must specify a base directory")
	}

	if opts.debug {
		log.SetLevel(log.DebugLevel)
	} else if opts.verbose {
		log.SetLevel(log.InfoLevel)
	}
}

func init() {
	flag.Usage = usage
}

func main() {
	parseFlags()

	// Convert the ~ (if any) into a home directory.
	basedir, _ := expand(opts.basedir)

	var _, err = os.Stat(basedir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: The base directory (%v) does not exist.\n\n", basedir)
		usage() // Usage exits.
	}

	telechats, err := fetchAgenda(opts.baseurl)
	if err != nil {
		log.Fatalf("ERROR: %v\n\n", err)
	}
	log.Infof("Telechats: %v", telechats)
	results := fetchDocs(basedir, telechats)
	for _, result := range results {
		if result != "" {
			fmt.Printf("%v\n", result)
		}
	}
	os.Exit(0)
}
