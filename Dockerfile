FROM golang:alpine AS build

RUN apk add git

RUN mkdir /src
ADD . /src
WORKDIR /src

RUN go build -o /tmp/server ./main.go

FROM alpine:edge

COPY --from=build /tmp/server /sbin/server

EXPOSE 8888

CMD sh -c "exec /sbin/server -server=$NATS_URL -creds=/tmp/nats.creds web"
