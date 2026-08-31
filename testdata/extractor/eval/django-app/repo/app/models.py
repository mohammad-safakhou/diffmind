from django.db import models


class Order(models.Model):
    total = models.IntegerField()
    status = models.CharField(max_length=32)
