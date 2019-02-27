FROM python:3.7-alpine

COPY main.py /root/warmup_sitemaps/main.py
WORKDIR /root/warmup_sitemaps/

RUN apk update && apk add libxml2-dev libxslt-dev gcc g++ make
RUN pip install --upgrade pip && pip install --upgrade -r requirements.txt
RUN chmod 755 /root/warmup_sitemaps/main.py
ENTRYPOINT ["/root/warmup_sitemaps/main.py"]