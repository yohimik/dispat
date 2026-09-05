FROM golang:1.24 AS build
ARG CORE_VERSION
WORKDIR /src
COPY go.mod .
RUN go mod edit -require=example.invalid/core@v${CORE_VERSION}
RUN go mod download example.invalid/core@v${CORE_VERSION}
COPY main.go .
RUN CGO_ENABLED=0 go build -o /out/api ./...

FROM scratch
COPY --from=build /out/api /api
ENTRYPOINT ["/api"]
