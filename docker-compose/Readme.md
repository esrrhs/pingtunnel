Deploy with docker-compose
===========================
 **First** edit `.env` file in this directory to your appropriate value.

**Then** run stack with these commands:

- in the server
```
docker-compose -f server.yml up -d
```
- in client machine
```
docker-compose -f client.yml up -d
```

**Now** use socks5 proxy at port `1080` of your client machine