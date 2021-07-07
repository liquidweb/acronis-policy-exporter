# Acronis Prometheus Exporter

Will use `/probe` endpoint:
https://prometheus.io/docs/guides/multi-target-exporter/


# query

The 'cache' directory will contain "byPolicy" and "byTenent" folders that cache data from the API. Should be able to pull a testable uniq_id out of one of these. Then to target:

```
curl localhost:9666/byTenant?target=GBEWPG
```

Or to target by uuidv4 policy:
```
curl localhost:9666/byPolicy?target=01FCB317-131F-0B3C-228D-F781E469348A
```


# Docker

## .env 
Use the .envrc file to set  the following environment variables. Setting up a folder in lastpass for these

```
ACRONIS_CLIENT_ID=xxx
ACRONIS_CLIENT_SECRET=yyy
```

## Build 
`make build` - builds the docker image locally
## Run
`make run` - runs the exporter in the docker image
## Shell
`make shell` - runs sh in the docker image
## exec
`make exec` - gives you a shell in a running docker image (for `make run`)
## test
`make test` - runs `go test` to run those tests against the codebase

