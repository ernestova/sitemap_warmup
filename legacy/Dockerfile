FROM python:3.11-slim

COPY main.py /root/warmup_sitemaps/main.py
COPY requirements.txt /root/warmup_sitemaps/requirements.txt
WORKDIR /root/warmup_sitemaps/

# requirements.txt pins every dependency, transitive ones included. Wheels
# only, so no package runs a setup script at build time.
RUN pip install --no-cache-dir --only-binary :all: -r /root/warmup_sitemaps/requirements.txt
RUN chmod 755 /root/warmup_sitemaps/main.py
ENTRYPOINT ["/root/warmup_sitemaps/main.py"]
