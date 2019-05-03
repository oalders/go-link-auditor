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

func main() {
	var verbose = flag.Bool("verbose", false, "turn on verbose mode")
	var host = flag.String("host", "", "host to crawl")
	flag.Parse()

	var links linkReport
	var heads = map[string]int{}

	// Dump a report if we are interrupted before running to completion.
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt)
	go func() {
		for sig := range channel {
			spew.Dump(sig)
			finishReport(links, heads)
			printReport(links)
			os.Exit(1)
		}
	}()

	u, _ := url.Parse(*host)

	c := mainCollector(u.Host, &links, heads, *verbose)

	// Visit the first page to kick start the robot
	// Error handling is in onError()
	_ = c.Visit(u.String())

	// Enable if async is true
	if c.Async {
		c.Wait()
	}

	finishReport(links, heads)

	log.Println("head report:")
	spew.Dump(heads)

	printReport(links)
}

func mainCollector(host string, links *linkReport, heads headReport, verbose bool) *colly.Collector {
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
		log.Printf("%s %s", r.Method, r.URL.String())
		r.Ctx.Put("url", r.URL.String())
		if r.Method == "GET" && r.URL.Host != "" && r.URL.Host != host {
			_ = c.Head(r.URL.String())
			log.Printf("aborting %v", r.URL)
			r.Abort()
		}
	})

	c.OnResponse(func(r *colly.Response) {
		log.Printf("OnResponse ------- %v %v", r.Request.URL, r.StatusCode)
		heads[r.Request.URL.String()] = r.StatusCode
		if r.Request.URL.String() != r.Ctx.Get("url") {
			heads[r.Ctx.Get("url")] = r.StatusCode
		}
		log.Printf("url from context %v", r.Ctx.Get("url"))
		if r.StatusCode > 299 || r.StatusCode < 399 {
			// errors are checked by this function
			log.Printf("link and code: %v %v", r.Request.URL, r.StatusCode)
			_ = c.Head(r.Headers.Get("Location"))
		}
		spew.Dump(r)
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
		if verbose {
			log.Printf("starting onHTML")
		}

		a := e.Request.AbsoluteURL(e.Attr("href"))
		spew.Dump(a)
		u, _ := url.Parse(a)

		var httpsLink string

		if u.Scheme == "http" {
			https, _ := url.Parse(u.String()) // deep copy
			https.Scheme = "https"
			httpsLink = https.String()
			_ = c.Visit(httpsLink)
		}

		row := []string{e.Request.URL.String(), u.String(), httpsLink}
		*links = append(*links, row)

		// Visit any subsequent links we find
		// Error handling happens in the collector's onError()
		log.Printf("adding %v to list of links to visit", u.String())
		_ = c.Visit(u.String())
	})

	return c
}

func finishReport(links linkReport, heads headReport) {
	spew.Dump(links)
	for i, v := range links {
		var ssl = v[2]

		// SSL links will not be available if the original page already has SSL
		if ssl != "" {
			log.Printf("ssl link %v", ssl)
			ssl = strconv.Itoa(heads[v[2]])
			log.Printf("ssl link %v", ssl)
		}
		v = append(v, strconv.Itoa(heads[v[1]]), ssl)

		links[i] = v
	}
}

func printReport(links linkReport) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Source Page", "Link", "SSL Link", "Link Status", "SSL Status"})
	table.AppendBulk(links)

	table.Render() // Send output
}
