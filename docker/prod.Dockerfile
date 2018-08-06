FROM alpine AS base
RUN apk add --no-cache make gcc musl-dev linux-headers libc6-compat

FROM golang:1.10.3 AS go-builder
WORKDIR /go
COPY . /go/
RUN go get -d -v .
RUN make deps
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -v -o main .

FROM base
COPY --from=go-builder /go/main /main
CMD ["/main"]
