package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"

	"github.com/davecgh/go-spew/spew"
	"github.com/gocolly/colly"
	"github.com/olekukonko/tablewriter"
)

// usage: go run robocop.go -verbose -host=https://example.com

func main() {

	// maybe create cache directory
	cacheDir := ".url-cache"
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		log.Printf("Cannot create dir %s because %v", cacheDir, err)
	}

	var verbose = flag.Bool("verbose", false, "turn on verbose mode")
	var host = flag.String("host", "", "host to crawl")
	flag.Parse()

	url, _ := url.Parse(*host)

	var report [][]string

	// Dump a report if we are interrupted before running to completion.
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt)
	go func() {
		for sig := range channel {
			spew.Dump(sig)
			printReport(report)
			os.Exit(1)
		}
	}()

	c := colly.NewCollector(
		colly.AllowedDomains(url.Host),
		colly.Async(true),
		colly.CacheDir(cacheDir),
	)
	c.AllowURLRevisit = false

	h := colly.NewCollector(
		colly.Async(false),
	)
	h.AllowURLRevisit = false

	c.OnResponse(func(r *colly.Response) {
		log.Printf("HEAD: %v %v", r.Request.URL, r.StatusCode)
	})

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		linkURL, _ := url.Parse(link)
		if linkURL.Scheme != "https" && linkURL.Scheme != "mailto" {
			linkURL.Scheme = "https"
			row := []string{e.Request.URL.String(), link, linkURL.String(), "500"}
			report = append(report, row)
		}

		// Visit any subsequent links we find
		if err := c.Visit(link); err != nil {
			if *verbose {
				log.Printf("cannot visit %s because of %v", link, err)
				if fmt.Sprintf("%v", err) == "Forbidden domain" {
					if *verbose {
						fmt.Printf("starting HEAD request for %v", link)
					}
					err = h.Head(link)
					if err != nil {
						log.Printf("cannot get HEAD request for %v because of %v", link, err)
					}
				}
			}
		}
	})

	if *verbose {
		c.OnRequest(func(r *colly.Request) {
			log.Printf("Visiting %s", r.URL.String())
		})
	}

	// Visit the first page to kick start the robot
	if err := c.Visit(url.String()); err != nil {
		log.Printf("cannot visit %s because of %v", url.String(), err)
	}
	c.Wait()
	h.Wait()
	printReport(report)
}

func printReport(report [][]string) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Page", "Outbound Link", "Secure Link", "Status Code"})
	table.AppendBulk(report)

	table.Render() // Send output
}
