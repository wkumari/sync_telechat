// This program read the upcoming IESG telechats, downloads PDF versions
// of each document, and places them in directories according to the date.
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
// CLM: Normally all 'const' and 'var' things at the global level are in:
/*

const (
 foo = 1
 bar = 3
)

var (
  bling = "thing"
  blaze = "thong"
)

*/
// String to use if the Telechat date is unknown.
const kUnknownTelechat = "Unknown-date"

// Where the IESG agenda lives.
const kUrl = "https://datatracker.ietf.org/iesg/agenda/documents/"

// Where the PDF versions of drafts live.
const kDocUrl = "https://tools.ietf.org/pdf/"

// Where I normally sync files.
const kBaseDir = "~/ownCloud/Goodreader/IESG/"

// Ugh! Globals.
// TODO[WK]: I started removing globals, confirm not used any more...
var date string

//Expand homedir - stolen from SO.
func expand(path string) (string, error) {
	// CLM I might use: https://golang.org/pkg/strings/#HasPrefix for the 'has ~' check.
	if len(path) == 0 || path[0] != '~' {
		return path, nil
	}

	// CLM I think the general 'go practice' is to use short names when things don't last long:
	// u, err := user.Current()
	// this is fine though, just sayin you'll often find single char vars ... if you don't care
	// that the var live longer than  a few lines then 1 char is fine.
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(usr.HomeDir, path[1:]), nil
}

// Helper: checks if a slice (array) already contains an item.
func contains(array []string, item string) bool {
	for _, entry := range array {
		if entry == item {
			return true
		}
	}
	return false
}

// CLM "is an" ?
// Checks if provided string an Internet Draft name?
func ExtractDoc(document string) string {
	if strings.HasPrefix(document, "draft-") {
		return document
	}
	return ""
}

// Figure out if a token is a link to a document
func isdoc(t html.Token) bool {
	// Iterate over all of the Token's attributes until we find an "doc"
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

func ExtractDate(telechat string) string {
	// Parses out and returns the date from a string. E.g:
	// "IESG telechat 2017-04-27."   -> "2017-04-27"
	// CLM use back-ticks in regexp creation... in case you end up needing verbatim content.
	r := regexp.MustCompile("IESG telechat (.*)")
	date := r.FindStringSubmatch(telechat)
	if date == nil {
		glog.Warning("Was not able to extract date from " + telechat)
		// Return unknown telechat so files can be placed somewhere!
		return kUnknownTelechat
	}
	return date[1]
}

// CLM docstring up above func block.
func fetch_doc(basedir string, date string,
	document string, done chan string) {
	// Fetches a single document, puts it in the directory
	// specified by date.
	// Notifies we are done by posting to done!
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
// CLM move the func documentation above the start of the block
func fetch_docs(basedir string, documents map[string][]string) []string {
	// Concurrently fetches documents.
	// Takes a map of slices, {"date": [doc1, doc2]} and
	// gets the documents.

	// CLM you might consider making this a seperate function.
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
	
	// CLM I would have (probably) done:
	// for date, docs := range documents{
	//   for _, doc := range docs {
	//   }
	// yours works, but isn't what I'd expected.
	// you are also making a LOT of channel workers...
	// normally you'd make like 'maxConcurrent' (10?)
	// I normally add 'all things to work on' to a channel,
	// then start kicking off worker threads to drain the queue.
	// you made your channel unbounded, so you could just add
	// all docs to the channel, then kick off N fetch_doc routines.
	//
	// Oh, though your channel here is just for results, not
	// for a list of items to work on... so you'd have to do a
	// bunch of refactor to do the above suggestion.
	// This method works, mind you if you have 1000 documents
	// you'll kick off near 1000 'workers' .. which could go badly.
	for date, _ = range documents {
		for _, document := range documents[date] {
			go fetch_doc(basedir, date, document, channel)
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
	// you'll proabbaly want to close(channel) too.
	return items
}

// CLM normally the func godoc stuff is above the function block:
// fetchIESGAgenda implements fetching of the agenda webpage, parsing that for .....
func fetch_iesg_agenda(url string) map[string][]string {
	// This goes off and fetches the webpage which contains the IESG agenda.
	// It then parses the page looking for dates, and downloads the documents
	// for each date, placing the documents in a directories according to the
	// date. This relies upon the format of the webpage not changing much :-)

	// This returns a "hash of arrays" - in Python it would be:
	// { "2017-01-01": ["doc1", "doc2"], ...}
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
				// CLM you might just regexp 'does this have /doc/... in it?' instead of calling
				// a seperate function. I suppose that means getting all of the element as a string though.
				if isdoc(t) {
					z.Next()
					docname := ExtractDoc(string(z.Text()))
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
				date = ExtractDate(h2string)
			default:
				continue
			}
		}
	}
	// Should never get here - we should return at tt == html.ErrorToken
	glog.Fatal("Returned at bottom of fetch_iesg_agenda ?!")
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
	flag.Parse()
}

func main() {

	// CLM These aren't necessary, the flags package will invent the vars for you.
	var basedir, baseurl string

	// CLM I also often do the flags processing in the global var() section. This might be
	// a bad 'python' habit though.
	flag.Usage = usage
	flag.StringVar(&basedir, "basedir", kBaseDir,
		"Base directory to put files. Makes date based directories here.")
	flag.StringVar(&baseurl, "agenda", kUrl,
		"Where the agenda lives")
	flag.Parse()

	//flag.Set("logtostderr", "true")
	flag.Set("stderrthreshold", "ERROR")

	// Convert the ~ (if any) into a home directory.
	basedir, _ = expand(basedir)

	// CLM fetchIESGAgenda - silly style rules about _ in function names, and cases for acronyms.
	telechats := fetch_iesg_agenda(baseurl)
	// CLM fetchDocs - same as above
	results := fetch_docs(basedir, telechats)
	for _, result := range results {
		if result != "" {
			fmt.Printf("%v\n", result)
		}
	}
	os.Exit(0)
}
