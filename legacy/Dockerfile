FROM python:slim

COPY main.py /root/warmup_sitemaps/main.py
COPY requirements.txt /root/warmup_sitemaps/requirements.txt
WORKDIR /root/warmup_sitemaps/

RUN pip install --upgrade pip && pip install --upgrade -r /root/warmup_sitemaps/requirements.txt
RUN chmod 755 /root/warmup_sitemaps/main.py
ENTRYPOINT ["/root/warmup_sitemaps/main.py"]