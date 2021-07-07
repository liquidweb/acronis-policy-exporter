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


## Deploy via Helm directly

Set up your `docker-registry` credentials:

```
kubectl create secret docker-registry lw-registry \
--docker-server=$CI_REGISTRY \
--docker-username=$CI_REGISTRY_USER \
--docker-password=$CI_REGISTRY_PASSWORD
```

Set up your acronis credentials:

```
envsubst < k8s-version/template-secrets.yaml | tee secrets.yaml
kubectl apply -f secrets.yaml
```

See your template to be applied with:
```
helm template .
```

Install it with:

```
helm install acronis-exporter .
```

But of course you can't see that cause `10/8` route for AnyConnect.

Of course, this is `helm uploaded` in CI, and then installed via flux in `github.com/liquidweb/mako-flux-state`.

## Kubeseal

https://github.com/bitnami-labs/sealed-secrets#helm-chart
https://github.com/helm/charts/tree/master/stable/sealed-secrets

https://medium.com/better-programming/encrypting-kubernetes-secrets-with-sealed-secrets-fe363149a211


To create docker registry credentials:

```bash
kubectl create secret docker-registry acronis-exporter-registry \
--dry-run=client -oyaml \
--docker-server=$CI_REGISTRY \
--docker-username=$CI_REGISTRY_USER \
--docker-password=$CI_REGISTRY_PASSWORD \
> registry-secrets.yaml
```

To pack the Acronis credentials:

```bash
envsubst < templates/secrets.yaml >secrets.yaml
```

To get the mako cert for sealing, you'll have to contact @mwineland directly.
With that, to `kubeseal` both:

```bash
kubeseal --format yaml \
--namespace acronis-exporter \
--cert ~/Downloads/mako.crt \
<registry-secrets.yaml >sealed-registry-secrets.yaml
```

Note, you have to have `ACRONIS_CLIENT_URL` `ACRONIS_CLIENT_ID` and `ACRONIS_CLIENT_SECRET` set to something valid for the below to work.
`ACRONIS_CLIENT_URL` is probably `https://us5-cloud.acronis.com` but depends on region.

```bash
envsubst <~/src/git.liquidweb.com/helm-charts/acronis-exporter/template-acronis-secrets.yaml | \
kubeseal \
--format yaml \
--namespace acronis-exporter \
--cert ~/.config/prod2.crt | \
tee ~/src/github.com/liquidweb/mako-flux-state/prod2/releases/acronis-exporter/sealed-acronis-secrets.yaml
```

Secrets that have been `kubeseal` can (and should) be committed to the repo.
