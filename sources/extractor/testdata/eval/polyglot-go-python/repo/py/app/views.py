from django.http import JsonResponse

from .models import Order


def order_list(request):
    orders = Order.objects.filter(status="open")
    return JsonResponse({"orders": list(orders.values())})


def order_detail(request, pk):
    order = Order.objects.get(pk=pk)
    return JsonResponse({"order": order.total})
