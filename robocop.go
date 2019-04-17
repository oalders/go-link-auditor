package main

import (
	"flag"
	"log"
	"net/url"
	"os"
	"os/signal"

	"github.com/davecgh/go-spew/spew"
	"github.com/gocolly/colly"
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
	spew.Dump(verbose)

	url, _ := url.Parse(*host)

	var report = make(map[string]string)

	// Dump a report if we are interrupted before running to completion.
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt)
	go func() {
		for sig := range channel {
			spew.Dump(sig)
			spew.Dump(report)
			os.Exit(1)
		}
	}()

	c := colly.NewCollector(
		colly.AllowedDomains(url.Host),
		colly.Async(true),
		colly.CacheDir(cacheDir),
	)
	c.AllowURLRevisit = false

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		linkURL, _ := url.Parse(link)
		if linkURL.Scheme != "https" && linkURL.Scheme != "mailto" {
			report[link] = e.Request.URL.String()
			if *verbose {
				log.Printf("scheme %s in URL %v is not https.", linkURL.Scheme, link)
			}
		}

		if err := c.Visit(link); err != nil {
			if *verbose {
				log.Printf("cannot visit %s because of %v", link, err)
			}
		}
	})

	if *verbose {
		c.OnRequest(func(r *colly.Request) {
			log.Printf("Visiting %s", r.URL.String())
		})
	}

	//// Set max Parallelism and introduce a Random Delay
	//c.Limit(&colly.LimitRule{
	//Parallelism: 2,
	//RandomDelay: 5 * time.Second,
	//})

	if err := c.Visit(url.String()); err != nil {
		log.Printf("cannot visit %s because of %v", url.String(), err)
	}
	c.Wait()

	// Print final report
	spew.Dump(report)
}
