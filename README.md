[![Build Status](https://travis-ci.org/ernestova/sitemap_warmup.svg?branch=master)](https://travis-ci.org/ernestova/sitemap_warmup)
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fernestova%2Fsitemap_warmup.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2Fernestova%2Fsitemap_warmup?ref=badge_shield)

# Sitemap WarmUp
Sitemap Validation, Checker and CDN Warmup

This tool will crawl any sitemap.xml, validate, parse and check each URL, if a sitemap index is found it will added for processing. 

It validate that each sitemaps.xml follows the XML schemas for sitemaps  http://www.sitemaps.org/schemas/sitemap/0.9/sitemap.xsd and for Sitemap index files http://www.sitemaps.org/schemas/sitemap/0.9/siteindex.xsd

Then it check each URL and return its HTTP response code, Time, Meta Robots, Cache Control. 

# Docker
```
docker run -v ${PWD}:/tmp/ ernestova/sitemap_warmup -s "https://www.domain.com/sitemap.xml"  -c 5 -d 1 -o -q 
```

# Parameters
* -c to set the concurrency of workers.
* -d to set maximum sitemaps to process
* -o to output the results of each sitemap index into its own CSV file. 
* -q to display only failed results.



## License
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fernestova%2Fsitemap_warmup.svg?type=large)](https://app.fossa.io/projects/git%2Bgithub.com%2Fernestova%2Fsitemap_warmup?ref=badge_large)