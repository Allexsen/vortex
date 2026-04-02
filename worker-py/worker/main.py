import os
import logging
import pika
import time
import sys
import json

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger(__name__)

QUEUE_NAME = "vortex:processing:pending"

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

def on_message(channel, method, properties, body):
    data = json.loads(body)
    logger.info("Received message: trace_id=%s url=%s", data.get("trace_id"), data.get("url"))
    channel.basic_ack(delivery_tag=method.delivery_tag)

def main():
    url = os.getenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")

    connection = connect_to_rabbitmq(url)
    channel = connection.channel()

    channel.queue_declare(queue=QUEUE_NAME, durable=True)
    channel.basic_qos(prefetch_count=1)
    channel.basic_consume(queue=QUEUE_NAME, on_message_callback=on_message)

    logger.info("Waiting for messages on %s", QUEUE_NAME)
    try:
        channel.start_consuming()
    except KeyboardInterrupt:
        logger.info("Shutting down gracefully...")
        channel.stop_consuming()
    finally:
        connection.close()

if __name__ == "__main__":
    main()