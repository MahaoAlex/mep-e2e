FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY server .

EXPOSE 8080 8081

CMD ["./server"]
