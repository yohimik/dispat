ARG API_VERSION
FROM acme/api:${API_VERSION}
COPY web/webassets/ /srv/webassets/
COPY sdk/dist/sdk.js /srv/webassets/sdk.js
ENTRYPOINT ["/api"]
