FROM python:3.11-slim

COPY main.py /root/warmup_sitemaps/main.py
COPY requirements.lock /root/warmup_sitemaps/requirements.lock
WORKDIR /root/warmup_sitemaps/

# requirements.lock pins every dependency, transitive ones included, with
# sha256 hashes. Wheels only, so no package runs a setup script at build time.
RUN pip install --no-cache-dir --require-hashes --only-binary :all: -r /root/warmup_sitemaps/requirements.lock \
    && chmod 755 /root/warmup_sitemaps/main.py
ENTRYPOINT ["/root/warmup_sitemaps/main.py"]
