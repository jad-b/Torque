from django.conf.urls import patterns, include, url
from django.contrib import admin

import api.urls

urlpatterns = patterns('',
    # Examples:
    # url(r'^$', 'crank_server.views.home', name='home'),
    # url(r'^blog/', include('blog.urls')),
    url(r'^api/', include(api.urls, namespace='api')),

    url(r'^admin/', include(admin.site.urls)),
)
