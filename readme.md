# Form Data (Traefik Middleware Plugin)

Modify incoming request form fields for both application/x-www-form-urlencoded and multipart/form-data bodies.

- Set: create or overwrite a form field
- Append: add a new value to a field without replacing existing values
- Delete: remove fields by key

This middleware preserves uploaded files for multipart/form-data requests while mutating only the form fields.


## How it works

- For Content-Type application/x-www-form-urlencoded: the plugin parses and mutates `req.PostForm` and rewrites the body.
- For Content-Type multipart/form-data: the plugin parses the multipart form, applies operations to the text fields, and reconstructs the multipart body, copying file parts untouched.

Notes
- Only requests with the above content types are modified. Others pass through unchanged.
- Multipart parsing uses a 32MB in-memory threshold.


## Installation (Static Config)

Enable the plugin in Traefik's static configuration (file, Helm values, or CLI). Example YAML:

```yaml
experimental:
  plugins:
    formdata:
      moduleName: github.com/zalbiraw/formdata
      version: v0.0.2
```

Alternatively, for local development (no GitHub fetch):

```yaml
experimental:
  localPlugins:
    formdata:
      moduleName: github.com/zalbiraw/formdata
```
Place the plugin source in `./plugins-local/src/github.com/zalbiraw/formdata` relative to the Traefik working directory.


## Configuration (Dynamic Config)

The middleware accepts the following fields under `plugin.formdata`:

- `set` (map[string]string): create or overwrite fields
- `append` (map[string]string): add an additional value to a field
- `delete` ([]string): remove fields by key

### File Provider example

```yaml
http:
  routers:
    demo:
      rule: Host(`demo.localhost`)
      entryPoints: [web]
      service: demo-svc
      middlewares: [formdata-mdw]

  services:
    demo-svc:
      loadBalancer:
        servers:
          - url: http://127.0.0.1:8080

  middlewares:
    formdata-mdw:
      plugin:
        formdata:
          set:
            foo: one
          append:
            foo: two
            bar: baz
          delete:
            - removeMe
```

### Kubernetes CRD example

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: formdata
  namespace: apps
spec:
  plugin:
    formdata:
      set:
        test123: value
      append:
        test456: value
      delete:
        - bar
```


## Examples

URL-encoded request

```bash
curl -i \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data 'foo=old&removeMe=yes' \
  http://demo.localhost/path
```

With the config above, the request body forwarded to your service becomes:

```
foo=one&foo=two&bar=baz
```

Multipart with file upload

```bash
curl -i \
  -F 'title=Old Title' \
  -F 'file=@/path/to/file.jpg' \
  http://demo.localhost/upload
```

Files are preserved; only form fields are mutated.
