from django.http import JsonResponse


def order_list(request):
    return JsonResponse({"orders": []})


def order_detail(request, pk):
    return JsonResponse({"order": pk})
