from django.http import JsonResponse


def products(request):
    return JsonResponse({"products": []})
