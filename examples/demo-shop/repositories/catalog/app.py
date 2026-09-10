from flask import Flask, jsonify
import redis

app = Flask(__name__)
catalog_cache = redis.Redis(host="catalog-cache", port=6379, db=0)


@app.get("/products")
def products():
    cached = catalog_cache.get("featured-products")
    if cached is None:
        catalog_cache.set("featured-products", "[]")
    return jsonify({"products": []})
