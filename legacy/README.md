# Legacy Python CLI (v0)

This directory holds the original Python `sitemap_warmup` CLI, frozen at tag
[`v0-python`](https://github.com/clarivo-co/sitewarm/tree/v0-python).
It is no longer maintained; the Go rewrite (`sitewarm`) replaces it.

To keep using it:

```
git checkout v0-python
pip install -r requirements.txt
python3 main.py -s "https://www.example.com/sitemap.xml" -c 5
```
