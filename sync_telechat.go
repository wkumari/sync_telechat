// This program read the upcoming IESG telechats, downloads PDF versions
// of each new document, and places them in directories according to the date.
package main

import (
	"flag"
	"fmt"
	"github.com/golang/glog"
	"golang.org/x/net/html"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
        "strings"
	"time"
)

const (
	// String to use if the Telechat date is unknown.
	kUnknownTelechat = "Unknown-date"

	// Where the IESG agenda lives.
	kUrl = "https://datatracker.ietf.org/iesg/agenda/documents/"

	// Where the PDF versions of drafts live.
	kDocUrl = "https://tools.ietf.org/pdf/"

	// Where I normally sync files.
	kBaseDir = "~/ownCloud/Goodreader/IESG/"
)

// Expand home directory. Only works on Unix type systems.
// No sure why Go doesn't include a helper for this (well, I am, I just don't
// agree :-)
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

// Helper: checks if a slice already contains an item.
func contains(array []string, item string) bool {
	for _, entry := range array {
		if entry == item {
			return true
		}
	}
	return false
}

// Checks if provided string is an Internet Draft name?
func extractDoc(document string) string {
	if strings.HasPrefix(document, "draft-") {
		return document
	}
	return ""
}

// Figure out if a token is a link to a document
func isDoc(t html.Token) bool {
	// Iterate over all of the Token's attributes until we find a "doc"
	for _, a := range t.Attr {
		// if this is an href, we check if the value is a document link.
		if a.Key == "href" {
			doc := a.Val
			if strings.HasPrefix(doc, "/doc/draft") {
				return true
			}
		}
	}
	return false
}

// Parses out and returns the date from a string. E.g:
// "IESG telechat 2017-04-27."   -> "2017-04-27"
func extractDate(telechat string) string {
	r := regexp.MustCompile(`IESG telechat (.*)`)
	date := r.FindStringSubmatch(telechat)
	if date == nil {
		glog.Warning("Was not able to extract date from " + telechat)
		// Return unknown telechat so files can be placed somewhere!
		return kUnknownTelechat
	}
	return date[1]
}

// Fetches a single document, puts it in the directory specified by date.
// Notifies we are done by posting to done!
func fetchDoc(basedir string, date string, document string, done chan string) {

	filename := document + ".pdf"
	url := kDocUrl + filename

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

// Concurrently fetches documents.
// Takes a map of slices, {"date": [doc1, doc2]} and
// gets the documents.
func fetchDocs(basedir string, documents map[string][]string) []string {

	// Make directories if not already exist
	for date, _ := range documents {
		err := os.Mkdir(filepath.Join(basedir, date), 0777)
		if err != nil && !os.IsExist(err) {
			glog.Fatal(fmt.Sprintf("Error making %v: %v", filepath.Join(basedir, date), err.Error()))
		}
	}

	// Channels
	doccount := 0
	var items []string
	channel := make(chan string)
	for date, _ := range documents {
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

// Fetches the webpage which contains the IESG agenda.
// It then parses the page looking for dates, and downloads the documents
// for each date, placing the documents in a directories according to the
// date. This relies upon the format of the webpage not changing much :-)

// This returns a "hash of arrays" - in Python it would be:
// { "2017-01-01": ["doc1", "doc2"], ...}
func fetchIESGAgenda(url string) map[string][]string {
	result := make(map[string][]string)

	// In case we find a document before we find the date...
	date := kUnknownTelechat

	resp, err := http.Get(url)
	if err != nil {
		glog.Error("ERROR: " + err.Error())
		return result
	}

	// We now parse the page.
	b := resp.Body
	defer b.Close() // close Body when the function returns

	z := html.NewTokenizer(b)

	for {
		tt := z.Next()

		switch {
		case tt == html.ErrorToken:
			// End of the document, we're done
			return result
		case tt == html.StartTagToken:
			t := z.Token()

			// Check if the token is an <a> tag or an <h2>
			switch {
			case t.Data == "a":
				if isDoc(t) {
					z.Next()
					docname := extractDoc(string(z.Text()))
					// Check if we already have this document listed for this date.
					// If a document is on multiple dates we will still download it
					// multiple times. Not worth fixing...
					if docname != "" && !contains(result[date], docname) {
						glog.Info("Found a new document: " + docname + " on " + date)
						result[date] = append(result[date], docname)
					}
				}

			case t.Data == "h2":
				// If we have an "h2" tag, we want the "content"
				z.Next()
				h2string := string(z.Text())
				date = extractDate(h2string)
			default:
				continue
			}
		}
	}
	// Should never get here - we should return at tt == html.ErrorToken
	glog.Fatal("Returned at bottom of fetchIESGAgenda ?!")
	return result
}

func usage() {
	fmt.Fprintf(os.Stderr, "Syncs the IESG Telechat to local directories.\n")
	fmt.Fprintf(os.Stderr, "NOTE: overrides --logtostderr to True.\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func init() {
	flag.Usage = usage
}

func main() {

	var basedir, baseurl string

	flag.Usage = usage
	flag.StringVar(&basedir, "basedir", kBaseDir,
		"Base directory to put files. Makes date based directories here.")
	flag.StringVar(&baseurl, "agenda", kUrl,
		"Where the agenda lives")
	flag.Parse()

	flag.Set("stderrthreshold", "ERROR")

	// Convert the ~ (if any) into a home directory.
	basedir, _ = expand(basedir)

	var _, err = os.Stat(basedir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: The base directory (%v) does not exist.\n\n", basedir)
		usage() // Usage exits.
	}

	telechats := fetchIESGAgenda(baseurl)
	results := fetchDocs(basedir, telechats)
	for _, result := range results {
		if result != "" {
			fmt.Printf("%v\n", result)
		}
	}
	os.Exit(0)
}
