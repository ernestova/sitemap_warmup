![Docker Badge](https://img.shields.io/docker/build/ernestova/sitemap_warmup.svg)

# Sitemap WarmUp
Sitemap Validation, Checker and CDN Warmup

This tool will crawl any sitemap.xml, validate, parse and check each URL, if a sitemap index is found it will added for processing. 

It validate that each sitemaps.xml follows the XML schemas for sitemaps  http://www.sitemaps.org/schemas/sitemap/0.9/sitemap.xsd and for Sitemap index files http://www.sitemaps.org/schemas/sitemap/0.9/siteindex.xsd

Then it check each URL and return its HTTP response code, Time, Meta Robots, Cache Control. 

# Docker
```
docker run -v .:/tmp/ ernestova/sitemap_warmup -s "https://www.domain.com/sitemap.xml"  -o -q -c 5 -d 1
```

# Paramters
-o to output the results of each sitemap index into its own CSV file. 
-q to display only failed results.
-c to set the concurrency of workers.
-d to set maximum sitemaps to process
