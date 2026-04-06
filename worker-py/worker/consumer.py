import os
import sys
import time
import pika
import json
from . import chunker
from prometheus_client import Counter, Histogram

import logging

logger = logging.getLogger(__name__)

MESSAGES_PROCESSED_TOTAL = Counter(
    "vortex_messages_processed_total",
    "Total number of messages processed",
    labelnames=["status"]
)

MESSAGE_PROCESS_LATENCY = Histogram(
    "vortex_message_process_latency_seconds",
    "Time to process a message",
    buckets=[0.1, 0.25, 0.5, 1, 2.5, 5, 10]
)


QUEUE_NAME = "vortex:processing:pending"

class MessageHandler:
    def __init__(self, model, db):
        self.model = model
        self.db = db
    
    def handle(self, channel, method, properties, body):
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

        self.channel.queue_declare(queue=QUEUE_NAME, durable=True)
        self.channel.basic_qos(prefetch_count=1)

        handler = MessageHandler(self.model, self.db)
        self.channel.basic_consume(queue=QUEUE_NAME, on_message_callback=handler.handle)

        logger.info("Waiting for messages on %s", QUEUE_NAME)
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