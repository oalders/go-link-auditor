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
type pageReport = map[string]map[string]string

func main() {
	var verbose = flag.Bool("verbose", false, "turn on verbose mode")
	var host = flag.String("host", "", "host to crawl")
	flag.Parse()

	var heads = map[string]int{}
	var pages = map[string]map[string]string{}

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

	c := makeColly(u.Host, heads, pages, *verbose)

	// Visit the first page to kick start the robot
	_ = c.Visit(u.String())

	// Enable if async is true
	if c.Async {
		c.Wait()
	}

	log.Println("head report:")
	spew.Dump(heads)

	printReport(finishReport(pages, heads))
}

func makeColly(host string, heads headReport, pages pageReport, verbose bool) *colly.Collector {
	// maybe create cache directory
	cacheDir := ".url-cache"
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		log.Printf("Cannot create dir %s because %v", cacheDir, err)
	}

	// XXX fix concurrent map writes before enabling async
	c := colly.NewCollector(
		colly.Async(false),
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
				log.Printf("aborting %v", r.URL)
			}
			r.Abort()
		}
	})

	c.OnResponse(func(r *colly.Response) {
		heads[r.Request.URL.String()] = r.StatusCode
		if r.Request.URL.String() != r.Ctx.Get("url") {
			heads[r.Ctx.Get("url")] = r.StatusCode
		}
		// Looks like we never hit this condition
		if r.StatusCode > 299 && r.StatusCode < 399 {
			fmt.Printf("redirecting  %v to %v\n", r.Ctx.Get("url"), r.Request.URL.String())
			heads[r.Ctx.Get("url")] = r.StatusCode
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

		pages[e.Request.URL.String()] = map[string]string{}
		pages[e.Request.URL.String()][foundURL.String()] = ""

		// Visit any subsequent links we find
		// Error handling happens in the collector's onError()
		if verbose {
			log.Printf("adding %v to list of links to visit", foundURL.String())
		}

		if foundURL.Host == host {

			// Avoid long URLs like Facebook share links
			foundURL.RawQuery = ""
			foundURL.Fragment = ""
		}
		_ = c.Visit(foundURL.String())
	})

	return c
}

func finishReport(pages pageReport, heads headReport) linkReport {
	rows := make([][]string, 0)

	for sourcePage := range pages {

		spew.Dump(pages[sourcePage])
		for link := range pages[sourcePage] {
			row := make([]string, 5)

			row[0] = sourcePage
			row[1] = link
			row[2] = strconv.Itoa(heads[link])

			linkURL, _ := url.Parse(link)
			if linkURL.Scheme == "http" {
				linkURL.Scheme = "https"
				row[3] = linkURL.String()
				row[4] = strconv.Itoa(heads[row[3]])
			}
			rows = append(rows, row)
		}
	}
	spew.Dump(rows)
	return rows
}

func printReport(rows linkReport) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Source Page", "Link", "Status", "SSL", "SSL Status"})
	table.AppendBulk(rows)

	table.Render() // Send output
}

// https://play.golang.org/p/EzvhWMljku
func truncateString(str string, num int) string {
	bnoden := str
	if len(str) > num {
		if num > 3 {
			num -= 3
		}
		bnoden = str[0:num] + "..."
	}
	return bnoden
}
