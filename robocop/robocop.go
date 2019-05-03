package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"

	"github.com/davecgh/go-spew/spew"
	"github.com/gocolly/colly"
	"github.com/olekukonko/tablewriter"
)

// usage: go run robocop.go -verbose -host=https://example.com

type linkReport [][]string
type headReport = map[string]int
type pageReport = map[string]map[string]map[string]string

func main() {
	var verbose = flag.Bool("verbose", false, "turn on verbose mode")
	var host = flag.String("host", "", "host to crawl")
	flag.Parse()

	var links linkReport
	var heads = map[string]int{}
	var pages = map[string]map[string]map[string]string{}

	// Dump a report if we are interrupted before running to completion.
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt)
	go func() {
		for sig := range channel {
			spew.Dump(sig)
			printReport(finishReport(pages, heads))
			os.Exit(1)
		}
	}()

	u, _ := url.Parse(*host)

	c := mainCollector(u.Host, &links, heads, pages, *verbose)

	// Visit the first page to kick start the robot
	// Error handling is in onError()
	_ = c.Visit(u.String())

	// Enable if async is true
	if c.Async {
		c.Wait()
	}

	log.Println("head report:")
	spew.Dump(heads)

	printReport(finishReport(pages, heads))
}

func mainCollector(host string, links *linkReport, heads headReport, pages pageReport, verbose bool) *colly.Collector {
	// maybe create cache directory
	cacheDir := ".url-cache"
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		log.Printf("Cannot create dir %s because %v", cacheDir, err)
	}

	// XXX fix concurrent map writes before enabling async
	c := colly.NewCollector(
		colly.Async(false),
		colly.CacheDir(cacheDir),
	)
	c.AllowURLRevisit = false
	c.ParseHTTPErrorResponse = true

	c.OnRequest(func(r *colly.Request) {
		r.Ctx.Put("url", r.URL.String())
		if r.Method == "GET" && r.URL.Host != "" && r.URL.Host != host {
			_ = c.Head(r.URL.String())
			if verbose {
				log.Printf("aborting %v", r.URL)
			}
			r.Abort()
		}

		var httpsLink string

		if r.URL.Scheme == "http" {
			https, _ := url.Parse(r.URL.String()) // deep copy
			https.Scheme = "https"
			httpsLink = https.String()
		}
		_ = c.Visit(httpsLink)
	})

	c.OnResponse(func(r *colly.Response) {
		heads[r.Request.URL.String()] = r.StatusCode
		if r.Request.URL.String() != r.Ctx.Get("url") {
			heads[r.Ctx.Get("url")] = r.StatusCode
		}
		if r.StatusCode > 299 || r.StatusCode < 399 {
			_ = c.Head(r.Headers.Get("Location"))
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		heads[r.Request.URL.String()] = r.StatusCode
		var link = r.Request.URL
		if verbose {
			log.Printf("cannot visit %s because of %v", link, err)
		}
		if fmt.Sprintf("%v", err) == "Forbidden domain" {
			if verbose {
				log.Printf("queuing HEAD request for %v\n", link)
			}

			// Error handling happens in OnError
			err := c.Head(link.String())
			if err != nil {
				log.Printf("HEAD queue error in c.OnError: %v", err)
			}

			// If the link is http, check if https is available
			if link.Scheme == "http" {
				link.Scheme = "https"
				_ = c.Head(link.String())
			}

		}
	})

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		a := e.Request.AbsoluteURL(e.Attr("href"))
		foundURL, _ := url.Parse(a)

		pages[e.Request.URL.String()] = map[string]map[string]string{}
		pages[e.Request.URL.String()][foundURL.String()] = nil

		// Visit any subsequent links we find
		// Error handling happens in the collector's onError()
		log.Printf("adding %v to list of links to visit", foundURL.String())
		_ = c.Visit(foundURL.String())
	})

	return c
}

func finishReport(pages pageReport, heads headReport) linkReport {
	//var links linkReport
	spew.Dump(pages)

	rows := make([][]string, 0)

	for url := range pages {

		spew.Dump(pages[url])
		//for linksOnPage, _ := range pages{url} {
		row := make([]string, 4)

		row[0] = url
		row[1] = strconv.Itoa(heads[url])
		rows = append(rows, row)
		//}

		// SSL links will not be available if the original page already has SSL
		//if ssl != "" {
		//ssl = strconv.Itoa(heads[v[2]])
		//}
		//v = append(v, strconv.Itoa(heads[v[1]]), ssl)

		//pages = append(pages,
		//links[i] = v
	}
	return rows
}

func printReport(rows linkReport) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Source Page", "Link", "SSL Link", "Link Status", "SSL Status"})
	table.AppendBulk(rows)

	table.Render() // Send output
}
