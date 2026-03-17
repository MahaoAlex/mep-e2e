FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY client .

CMD ["./client"]
