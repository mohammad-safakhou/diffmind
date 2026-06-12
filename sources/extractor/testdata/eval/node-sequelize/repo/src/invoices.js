const express = require("express");
const { Invoice } = require("./models");

const app = express();

app.get("/invoices/:id", async (req, res) => {
  const invoice = await Invoice.findByPk(req.params.id);
  res.json(invoice);
});

app.post("/invoices", async (req, res) => {
  const invoice = await Invoice.create(req.body);
  res.status(201).json(invoice);
});

app.listen(3000);
