#!/usr/bin/env python3
import csv
import os
import sys
import datetime
import argparse
import functools
import asyncio
import aiohttp
import requests
import csv
from lxml import etree, html
from tabulate import tabulate
from urllib.parse import urlparse

parser = argparse.ArgumentParser(description='Asynchronous CDN Cache Warmer')
parser.add_argument('-s', '--site', action="append", dest='sites', default=None)
parser.add_argument('-d', '--depth', action="store", dest='depth', default=None)
parser.add_argument('-c', '--concurrency', action="store", dest='concurrency', default=1)
parser.add_argument('-o', '--output',  action="store_true", default=None)
parser.add_argument('-q', '--quiet', action="store_true", help="Only print 40x, 50x or 200 with noindex")
args = parser.parse_args()
concurrency = args.concurrency
depth = args.depth
sites = args.sites
quiet = args.quiet
output = args.output

# Colorize #
red = '\033[0;31m'
green = '\033[0;32m'
no_color = '\033[0m'

tasks = []
results = []
time_array = []
failed_links = 0
success_links = 0


def get_links(mage_links):
    r = requests.get(mage_links)
    if "200" not in str(r):
        sys.exit(red + "Sitemap fetch failed for %s with %s. Exiting..." % (mage_links, r) + no_color)
    root = etree.fromstring(r.content)
    print("The number of sitemap tags are %s" % str((len(root))))
    links = []
    for sitemap in root:
        prefix, tag = sitemap.tag.split("}")
        children = sitemap.getchildren()
        if tag == 'sitemap':
            print("Sitemap Index found %s" % children[0].text)
            sites.append(children[0].text)
        else:
            links.append(children[0].text)
    return links


async def bound_warms(sem, url):
    async with sem:
        await warm_it(url)


async def warm_it(url):

    connection_started_time = None
    connection_made_time = None

    class TimedResponseHandler(aiohttp.client_proto.ResponseHandler):
        def connection_made(self, transport):
            nonlocal connection_made_time
            connection_made_time = datetime.datetime.now()
            return super().connection_made(transport)

    class TimedTCPConnector(aiohttp.TCPConnector):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, **kwargs)
            self._factory = functools.partial(TimedResponseHandler, loop=loop)

        async def _create_connection(self, req, traces, timeout):
            nonlocal connection_started_time
            connection_started_time = datetime.datetime.now()
            return await super()._create_connection(req, traces, timeout)

    async with aiohttp.ClientSession(connector=TimedTCPConnector(loop=loop)) as session:
        async with session.get(url) as response:
            global dot, dot_total
            time_delta = connection_made_time - connection_started_time
            time_taken = "%s sec %s ms" % (time_delta.seconds, time_delta.microseconds)

            robots_status = ""
            if response.status != 200:
                response_output = red + str(response.status) + no_color
            else:
                response_output = green + str(response.status) + no_color
                time_array.append(time_delta)
                doc = html.fromstring(await response.text())
                robots = doc.xpath("//meta[@name='robots']/@content")
                if len(robots) > 0:
                    robots_status = robots[0]

            if (quiet is False) or (quiet is True and response.status != 200):
                results.append([url, response_output, time_taken, robots_status, response.headers['Cache-Control']])

            dot += 1
            if dot == 100:
                dor = ". %i\n" % dot_total
                dot = 0
                dot_total += 100
            else:
                dor = '.'

            print(dor, end='', flush=True)
            del doc, robots, response



def write_list_to_csv(csv_file, csv_columns, data_list):
    a = urlparse(csv_file)
    url_file = os.path.basename(a.path)
    filename = url_file.split('.')

    with open('/tmp/%s.csv' % filename[0], 'w') as csvfile:
        writer = csv.writer(csvfile, dialect='excel', quoting=csv.QUOTE_NONNUMERIC)
        writer.writerow(csv_columns)
        for data in data_list:
            writer.writerow(data)


if concurrency is None:
    print("The concurrency limit isn't specified. Setting limit to 150")
else:
    print("Setting concurrency limit to %s" % concurrency)
    print("Quiet: %s" % quiet)
    print("Output: %s" % output)

sem = asyncio.Semaphore(int(concurrency))
loop = asyncio.get_event_loop()

iteration = 0
while sites:

    dot = 0
    dot_total = 0
    tasks = []
    results = []
    time_array = []
    failed_links = 0
    success_links = 0

    current_sites = sites.pop(0)
    print("#############################################################################################")
    print("Processing %s" % current_sites)

    mage_links = get_links(str(current_sites))

    if len(mage_links) > 0:
        for i in mage_links:
            task = asyncio.ensure_future(bound_warms(sem, i))
            tasks.append(task)
        loop.run_until_complete(asyncio.wait(tasks))

        for res in results:
            if "200" in res[1]:
                success_links += 1
            else:
                failed_links += 1

        avg_time = str((sum([x.total_seconds() for x in time_array]))/len(time_array))

        tab_headers = ['URL', 'Response code', 'Time', 'Meta Robots', 'Cache Control']

        print("\n")
        print(tabulate(results, showindex=True,
                       headers=tab_headers))
        print(tabulate([[str(failed_links), str(success_links), avg_time]],
                       headers=['Failed', 'Successful', 'Average Time']))

        if output is True:
            write_list_to_csv(current_sites, tab_headers, results)

        iteration += 1
        if (depth is not False) and iteration == depth:
            print("Depth level of %i reach" % iteration)
            exit()

        del results
        del mage_links

    print("END INTERATION %i \n" % iteration)
