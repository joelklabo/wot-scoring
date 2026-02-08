FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /wot-scoring .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /wot-scoring /usr/local/bin/wot-scoring
EXPOSE 8090
ENTRYPOINT ["wot-scoring"]
