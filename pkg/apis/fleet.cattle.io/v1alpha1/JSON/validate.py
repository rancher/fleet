from jsonschema import Draft202012Validator, exceptions
import json

with open("fleet-combined.schema.json") as f:
    schema = json.load(f)

try:
    Draft202012Validator.check_schema(schema)
    print("Schema is valid JSON Schema (Draft 2020-12).")
except exceptions.SchemaError as e:
    print("Schema is invalid:", e.message)