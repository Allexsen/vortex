import time
import psycopg2
import logging
from pgvector.psycopg2 import register_vector

logger = logging.getLogger(__name__)

class Database:
    def __init__(self, url, max_retries, retry_delay):
        for attempt in range(1, max_retries + 1):
            try:
                self.connection = psycopg2.connect(url)
                register_vector(self.connection)
                logger.info("Connected to PostgreSQL")
                break
            except Exception as e:
                logger.warning("PostgreSQL not ready, attempt %d/%d: %s", attempt, max_retries, e)
                if attempt < max_retries:
                    time.sleep(retry_delay)
        else:
            logger.error("Failed to connect to PostgreSQL after %d attempts", max_retries)
            raise ConnectionError("Could not connect to PostgreSQL")


    def insert_article(self, trace_id, url, content):
        cursor = self.connection.cursor()
        cursor.execute("INSERT INTO articles (trace_id, url, content) VALUES (%s, %s, %s) RETURNING id", (trace_id, url, content))
        result = cursor.fetchone()
        if result is None:
            raise Exception("Insert failed - no id returned")
        return result[0]

    def insert_chunks(self, article_id, chunks, embeddings):
        cursor = self.connection.cursor()
        for i, chunk in enumerate(chunks):
            cursor.execute("INSERT INTO chunks (article_id, chunk_index, chunk_text, embedding) VALUES (%s, %s, %s, %s)",
                            (article_id, i, chunk, embeddings[i]))

    def commit(self):
        self.connection.commit()

    def rollback(self):
        self.connection.rollback()

    def close(self):
        self.connection.close()
