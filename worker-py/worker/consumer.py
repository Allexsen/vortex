import os
import sys
import time
import pika
import json

import redis
from . import chunker
from prometheus_client import Counter, Histogram

import logging

logger = logging.getLogger(__name__)

MESSAGES_PROCESSED_TOTAL = Counter(
    "vortex_embedder_messages_processed_total",
    "Total number of messages processed",
    labelnames=["status"]
)

MESSAGE_PROCESS_LATENCY = Histogram(
    "vortex_embedder_message_process_latency_seconds",
    "Time to process a message",
    buckets=[0.1, 0.25, 0.5, 1, 2.5, 5, 10]
)


CONTROL_KEY = "vortex:control:embedder"

EXCHANGE = "vortex.dlx"
PROCESSING_QUEUE = "vortex.processing.pending"
PROCESSING_DLQ = "vortex.processing.dlq"
PROCESSING_DLQ_ROUTING_KEY = "processing.dead"


class MessageHandler:
    def __init__(self, model, db, redis_client):
        self.model = model
        self.db = db
        self.redis_client = redis_client
    
    def _wait_if_paused(self, channel):
        while True:
            try:
                value = self.redis_client.get(CONTROL_KEY)
            except redis.RedisError as e:
                logger.warning("Failed to read embedder control key: %s", e)
                return # fail-open: if Redis is unavailable, don't block processing
            
            if value != b"pause":
                return
            channel.connection.sleep(1.0)

    def handle(self, channel, method, properties, body):
        self._wait_if_paused(channel)

        start = time.time()

        data = json.loads(body)
        trace_id = data.get("trace_id")
        url = data.get("url")
        logger.info("Received message: trace_id=%s url=%s", trace_id, url)

        try:
            content = data.get("content", "")
            if not content:
                MESSAGES_PROCESSED_TOTAL.labels(status="no_content").inc()
                logger.warning("No content found for trace_id=%s url=%s", trace_id, url)
                channel.basic_ack(delivery_tag=method.delivery_tag)
                return
            chunks = chunker.chunk_text(content)
            embeddings = self.model.encode(chunks)
            article_id = self.db.insert_article(trace_id, url, content)
            self.db.insert_chunks(article_id, chunks, embeddings)
            self.db.commit()
            logger.info("Processed and stored article: trace_id=%s url=%s", trace_id, url)
            channel.basic_ack(delivery_tag=method.delivery_tag)
            MESSAGES_PROCESSED_TOTAL.labels(status="success").inc()
        except Exception as e:
            MESSAGES_PROCESSED_TOTAL.labels(status="error").inc()
            logger.error("Error processing message: trace_id=%s url=%s error=%s", trace_id, url, e)
            self.db.rollback()
            channel.basic_nack(delivery_tag=method.delivery_tag, requeue=False)
        finally:
            MESSAGE_PROCESS_LATENCY.observe(time.time() - start)

class Consumer:
    def __init__(self, model, db):
        self.model = model
        self.db = db
        self.channel = None
        self.connection = None

    def start(self):
        url = os.getenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
        self.connection = connect_to_rabbitmq(url)
        self.channel = self.connection.channel()

        self.channel.exchange_declare(exchange=EXCHANGE, exchange_type="direct", durable=True)
        self.channel.queue_declare(
            queue=PROCESSING_QUEUE,
            durable=True,
            arguments={
                "x-dead-letter-exchange": EXCHANGE,
                "x-dead-letter-routing-key": PROCESSING_DLQ_ROUTING_KEY
            }
        )

        self.channel.queue_declare(
            queue=PROCESSING_DLQ,
            durable=True,
        )
        self.channel.queue_bind(queue=PROCESSING_DLQ, exchange=EXCHANGE, routing_key=PROCESSING_DLQ_ROUTING_KEY)
        self.channel.basic_qos(prefetch_count=1)

        redis_addr = os.getenv("REDIS_ADDR", "localhost:6379")
        redis_host, redis_port = redis_addr.split(":", 1)
        redis_client = redis.Redis(
            host=redis_host,
            port=int(redis_port),
            db=int(os.getenv("REDIS_DB", "0")),
            password=os.getenv("REDIS_PASSWORD", ""),
            socket_timeout=2.0
        )

        handler = MessageHandler(self.model, self.db, redis_client)
        self.channel.basic_consume(queue=PROCESSING_QUEUE, on_message_callback=handler.handle)

        logger.info("Waiting for messages on %s", PROCESSING_QUEUE)
        try:
            self.channel.start_consuming()
        finally:
            self.connection.close()
            logger.info("Consumer stopped")

    def stop(self):
        if self.channel:
            self.channel.stop_consuming()

def connect_to_rabbitmq(url):
    for attempt in range(1, 4):
        try:
            connection = pika.BlockingConnection(pika.URLParameters(url))
            logger.info("Connected to RabbitMQ")
            return connection
        except Exception as e:
            logger.warning("RabbitMQ not ready, attempt %d/3: %s", attempt, e)
            if attempt < 3:
                time.sleep(5)
    else:
        logger.error("Failed to connect to RabbitMQ after 3 attempts")
        sys.exit(1)