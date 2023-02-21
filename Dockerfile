FROM golang:1.19.5 AS builder
RUN go version
ARG PROJECT_VERSION

COPY . /go/src/github.com/lostz/copydir/
WORKDIR  /go/src/github.com/lostz/copydir/

RUN set -Eeux && \
    go mod download && \
    go mod verify

RUN GOOS=linux GOARCH=amd64 \
    go build \
    -trimpath \
    -ldflags="-w -s -X 'main.Version=${PROJECT_VERSION}'" \
    -o app main.go

FROM scratch
WORKDIR /root/
COPY --from=builder /go/src/github.com/lostz/copydir/app .

ENTRYPOINT ["./app"]

