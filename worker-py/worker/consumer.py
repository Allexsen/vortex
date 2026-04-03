import os
import sys
import time
import pika
import json
from . import chunker

import logging

logger = logging.getLogger(__name__)

QUEUE_NAME = "vortex:processing:pending"

class MessageHandler:
    def __init__(self, model, db):
        self.model = model
        self.db = db
    
    def handle(self, channel, method, properties, body):
        data = json.loads(body)
        trace_id = data.get("trace_id")
        url = data.get("url")
        logger.info("Received message: trace_id=%s url=%s", trace_id, url)

        try:
            content = data.get("content", "")
            if not content:
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
        except Exception as e:
            logger.error("Error processing message: trace_id=%s url=%s error=%s", trace_id, url, e)
            self.db.rollback()
            channel.basic_nack(delivery_tag=method.delivery_tag, requeue=False)

def start(model, db):
    url = os.getenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
    connection = connect_to_rabbitmq(url)
    channel = connection.channel()

    channel.queue_declare(queue=QUEUE_NAME, durable=True)
    channel.basic_qos(prefetch_count=1)

    handler = MessageHandler(model, db)
    channel.basic_consume(queue=QUEUE_NAME, on_message_callback=handler.handle)

    logger.info("Waiting for messages on %s", QUEUE_NAME)
    try:
        channel.start_consuming()
    except KeyboardInterrupt:
        logger.info("Shutting down gracefully...")
        channel.stop_consuming()
    finally:
        db.close()
        connection.close()

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