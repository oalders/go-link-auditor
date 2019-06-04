package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/gocolly/colly"
	"github.com/olekukonko/tablewriter"
)

// usage: go run robocop.go -verbose -host=https://example.com

type linkReport [][]string
type headReport = map[string]int
type pageReport = map[string]map[string]string

func main() {
	var maxVisits, randomDelay int
	var csv, onlyFailures, verbose bool
	var host string

	flag.IntVar(&randomDelay, "random-delay", 1, "random delay (in seconds)")
	flag.IntVar(&maxVisits, "max-visits", 10000, "maximum number of pages to scrape")
	flag.BoolVar(&csv, "csv", false, "dump data in CSV format")
	flag.BoolVar(&onlyFailures, "only-failures", false, "show only failures")
	flag.BoolVar(&verbose, "verbose", false, "turn on verbose mode")
	flag.StringVar(&host, "host", "", "host to crawl")
	flag.Parse()

	var heads = map[string]int{}
	var pages = map[string]map[string]string{}

	// Dump a report if we are interrupted before running to completion.
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt)
	go func() {
		for sig := range channel {
			spew.Dump(sig)
			//printReport(finishReport(pages, heads))
			os.Exit(1)
		}
	}()

	u, _ := url.Parse(host)

	c := makeColly(u.Host, heads, pages, &maxVisits, randomDelay, verbose)

	// Visit the first page to kick start the robot
	_ = c.Visit(u.String())
	maxVisits--

	// Enable if a(sync is true
	if c.Async {
		c.Wait()
	}

	log.Println("head report:")

	rows := finishReport(pages, heads, onlyFailures)
	printReport(rows)
	if csv {
		rows2csv(rows)
	}
}

func makeColly(
	host string,
	heads headReport,
	pages pageReport,
	maxVisits *int,
	randomDelay int,
	verbose bool,
) *colly.Collector {

	// Use this for multiple maps. Not worried about contention.
	var m = sync.Mutex{}

	// maybe create cache directory
	cacheDir := ".url-cache"
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		log.Printf("Cannot create dir %s because %v", cacheDir, err)
	}

	c := colly.NewCollector(
		colly.Async(true),
		colly.CacheDir(cacheDir),
		colly.DisallowedDomains("facebook.com"),
	)

	c.AllowURLRevisit = false
	c.ParseHTTPErrorResponse = true

	c.OnRequest(func(r *colly.Request) {
		r.Ctx.Put("url", r.URL.String())
		if r.Method == "GET" && r.URL.Host != "" && r.URL.Host != host {
			_ = c.Head(r.URL.String())
			if verbose {
				log.Printf("HEAD %v", r.URL)
			}
			r.Abort()
			return
		}
		m.Lock()
		if *maxVisits > 0 || r.Method == "HEAD" {
			fmt.Printf("max visits is %v %v %v\n", *maxVisits, r.Method, r.URL.String())
			if r.Method == "GET" {
				*maxVisits--
			}
		} else {
			if verbose {
				log.Printf("aborting %v over max visits", r.URL)
			}
			r.Abort()
		}
		m.Unlock()
	})

	c.OnResponse(func(r *colly.Response) {
		m.Lock()
		heads[r.Request.URL.String()] = r.StatusCode
		m.Unlock()

		if r.Request.URL.String() != r.Ctx.Get("url") {
			heads[r.Ctx.Get("url")] = r.StatusCode
		}
		// Looks like we never hit this condition :(
		if r.StatusCode > 299 && r.StatusCode < 399 {
			fmt.Printf("redirecting  %v to %v\n", r.Ctx.Get("url"), r.Request.URL.String())
			heads[r.Ctx.Get("url")] = r.StatusCode
			_ = c.Head(r.Headers.Get("Location"))
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		m.Lock()
		heads[r.Request.URL.String()] = r.StatusCode
		m.Unlock()

		var link = r.Request.URL
		if verbose {
			log.Printf("cannot visit %s because of %v", link, err)
		}
		if fmt.Sprintf("%v", err) == "Forbidden domain" {
			if verbose {
				log.Printf("queuing HEAD request for %v\n", link)
			}

			_ = c.Head(link.String())

			// If the link is http, check if https is available
			if link.Scheme == "http" && r.Request.Method == "HEAD" {
				link.Scheme = "https"
				_ = c.Head(link.String())
			}
		}
	})

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		a := e.Request.AbsoluteURL(e.Attr("href"))
		foundURL, _ := url.Parse(a)

		if foundURL.Scheme == "mailto" {
			if verbose {
				log.Printf("Skipping %v", foundURL.String())
			}
			return
		}

		m.Lock()
		u := e.Request.URL.String()

		if _, ok := pages[u]; !ok {
			pages[e.Request.URL.String()] = map[string]string{}
		}
		pages[e.Request.URL.String()][foundURL.String()] = ""
		m.Unlock()

		// Visit any subsequent links we find
		// Error handling happens in the collector's onError()
		if verbose {
			log.Printf("adding %v to list of links to GET", foundURL.String())
		}

		//if foundURL.Host == host {
		//// Avoid long URLs like Facebook share links
		//foundURL.RawQuery = ""
		//foundURL.Fragment = ""
		//}

		_ = c.Visit(foundURL.String())
	})

	_ = c.Limit(&colly.LimitRule{
		DomainGlob:  host,
		Parallelism: 2,
		RandomDelay: time.Duration(randomDelay) * time.Second,
	})

	return c
}

/*
Report format:
source page | link found on page | link status code | HTTPS link (if previous link HTTP) | HTTPS link status code
*/

func finishReport(pages pageReport, heads headReport, onlyFailures bool) linkReport {
	rows := make([][]string, 0)

	// Weed out success URLs for now
	for sourcePage := range pages {

		for link := range pages[sourcePage] {
			row := make([]string, 5)

			linkStatusCode := heads[link]

			// XXX find out why some HEAD requests aren't happening
			if linkStatusCode == 200 || linkStatusCode == 0 {
				continue
			}

			row[0] = sourcePage
			row[1] = link
			row[2] = strconv.Itoa(linkStatusCode)

			linkURL, _ := url.Parse(link)
			if linkURL.Scheme == "http" {
				linkURL.Scheme = "https"
				httpsLinkStatusCode := heads[row[3]]
				if onlyFailures && httpsLinkStatusCode == 200 {
					continue
				}

				if httpsLinkStatusCode != 0 {
					row[4] = strconv.Itoa(httpsLinkStatusCode)
				}
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func printReport(rows linkReport) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Source Page", "Link", "Status", "SSL Status"})
	table.AppendBulk(rows)

	table.Render() // Send output
}

func rows2csv(rows linkReport) {

	{
		w := csv.NewWriter(os.Stdout)
		_ = w.WriteAll(rows) // calls Flush internally

		if err := w.Error(); err != nil {
			log.Fatalln("error writing csv:", err)
		}
	}

	{

		file, err := os.OpenFile(
			"report.csv",
			os.O_CREATE|os.O_WRONLY,
			0666,
		)

		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		w := csv.NewWriter(file)
		_ = w.WriteAll(rows) // calls Flush internally

		if err := w.Error(); err != nil {
			log.Fatalln("error writing csv:", err)
		}
	}
}
